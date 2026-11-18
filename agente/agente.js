'use strict';
// Agente de consumo de Claude Code.
// Lee los registros locales (~/.claude/projects/**/*.jsonl), suma tokens por día y modelo,
// y envía SOLO esos totales al receptor central. Nunca envía prompts, código ni rutas.
//
// Uso:  node agente.js [--dry-run] [--completo]
//   --dry-run   muestra lo que enviaría, sin enviar
//   --completo  recalcula todo el historial (por defecto: últimos N días)

const fs = require('fs');
const path = require('path');
const os = require('os');
const crypto = require('crypto');
const cp = require('child_process');

const VERSION = '1.2.0';
const DIR = __dirname;
const ARGS = process.argv.slice(2);
const DRY = ARGS.includes('--dry-run');
const LOG_FILE = path.join(DIR, 'agente.log');
const ESTADO_FILE = path.join(DIR, 'estado.json');

function log(msg) {
  const line = `[${new Date().toISOString()}] ${msg}`;
  console.log(line);
  try {
    if (fs.existsSync(LOG_FILE) && fs.statSync(LOG_FILE).size > 1024 * 1024) fs.writeFileSync(LOG_FILE, '');
    fs.appendFileSync(LOG_FILE, line + '\n');
  } catch { /* sin log en disco */ }
}

function leerJson(file, def) {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')); } catch { return def; }
}

// ---------- Identidad de la máquina ----------
function machineGuid() {
  if (process.platform !== 'win32') return '';
  try {
    const out = cp.execFileSync('reg', ['query', 'HKLM\\SOFTWARE\\Microsoft\\Cryptography', '/v', 'MachineGuid'],
      { encoding: 'utf8', windowsHide: true });
    const m = out.match(/MachineGuid\s+REG_SZ\s+(\S+)/i);
    return m ? m[1] : '';
  } catch { return ''; }
}

function identidad() {
  const hostname = os.hostname();
  let usuario = '';
  try { usuario = os.userInfo().username; } catch { usuario = process.env.USERNAME || ''; }
  const guid = machineGuid();
  // Hostname + usuario se incluyen porque las VMs clonadas suelen repetir el MachineGuid.
  const machine_id = crypto.createHash('sha256').update(`${guid}|${hostname}|${usuario}`.toLowerCase()).digest('hex').slice(0, 24);
  return { machine_id, hostname, usuario };
}

// Correo de la sesión guardada en un .claude.json (oauthAccount.emailAddress).
function cuentaDe(rutas) {
  for (const r of rutas) {
    const j = leerJson(r, null);
    const email = j && j.oauthAccount && j.oauthAccount.emailAddress;
    if (email) return String(email).toLowerCase();
  }
  return '';
}

// Carpetas de configuración de Claude Code del usuario: la por defecto (~/.claude, cuenta en
// ~/.claude.json) y cualquier otra con "projects" y ".claude.json" (creadas con CLAUDE_CONFIG_DIR).
function perfilesClaude() {
  const casa = os.homedir();
  const def = path.join(casa, '.claude');
  const cands = [def];
  try { for (const e of fs.readdirSync(casa, { withFileTypes: true })) if (e.isDirectory()) cands.push(path.join(casa, e.name)); } catch { /* ignorar */ }
  cands.push(path.join(casa, '.config', 'claude'));
  if (process.env.CLAUDE_CONFIG_DIR) cands.push(process.env.CLAUDE_CONFIG_DIR);
  const vistos = new Set(), out = [];
  for (const d of cands) {
    const k = path.resolve(d).toLowerCase();
    if (vistos.has(k)) continue;
    vistos.add(k);
    try { if (!fs.statSync(path.join(d, 'projects')).isDirectory()) continue; } catch { continue; }
    const esDef = k === path.resolve(def).toLowerCase();
    if (!esDef && !fs.existsSync(path.join(d, '.claude.json'))) continue;
    const rutas = esDef ? [path.join(casa, '.claude.json'), path.join(d, '.claude.json')] : [path.join(d, '.claude.json')];
    out.push({ dir: d, cuenta: cuentaDe(rutas) });
  }
  return out;
}

const ocultar = (c, objetivo) => !c ? 'sin sesion' : c === objetivo ? c : '***' + c.slice(c.indexOf('@'));

// ---------- Lectura de registros ----------

function listarJsonl(dir, out) {
  let entradas;
  try { entradas = fs.readdirSync(dir, { withFileTypes: true }); } catch { return out; }
  for (const e of entradas) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) listarJsonl(p, out);
    else if (e.isFile() && e.name.endsWith('.jsonl')) {
      try { out.push({ p, mtime: fs.statSync(p).mtimeMs }); } catch { /* ignorar */ }
    }
  }
  return out;
}

function diaLocal(d) {
  const z = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${z(d.getMonth() + 1)}-${z(d.getDate())}`;
}

// <proyecto>/<id>.jsonl o <proyecto>/<id>/subagents/<agente>.jsonl
function claveSesion(p) {
  const dir = path.dirname(p);
  return path.basename(dir).toLowerCase() === 'subagents' ? path.basename(path.dirname(dir)) : path.basename(p, '.jsonl');
}

// Ruta relativa al perfil del usuario (no se envía el nombre de usuario).
function carpetaRelativa(cwd) {
  if (!cwd) return '';
  const rel = path.relative(os.homedir(), cwd);
  if (!rel) return '~';
  return rel.startsWith('..') || path.isAbsolute(rel) ? cwd : '~\\' + rel;
}

function recolectar(desdeDia, carpetas) {
  const desdeMs = desdeDia ? new Date(`${desdeDia}T00:00:00`).getTime() : 0;
  // Se relee la sesión completa si alguno de sus archivos cambió dentro de la ventana.
  const grupos = new Map(), recientes = new Set();
  for (const { p, mtime } of carpetas.flatMap(d => listarJsonl(d, []))) {
    const k = claveSesion(p);
    if (!grupos.has(k)) grupos.set(k, []);
    grupos.get(k).push(p);
    if (mtime >= desdeMs) recientes.add(k);
  }
  const mensajes = new Map(), sesiones = new Map();
  const ses = id => {
    if (!sesiones.has(id)) sesiones.set(id, { id, por_modelo: {}, mensajes: 0, mensajes_subagente: 0 });
    return sesiones.get(id);
  };
  let archivos = 0;
  for (const [k, rutas] of grupos) {
    if (!recientes.has(k)) continue;
    for (const file of rutas.sort()) {
      let texto;
      try { texto = fs.readFileSync(file, 'utf8'); } catch { continue; }
      archivos++;
      const esSub = /[\\/]subagents[\\/]/i.test(file);
      for (const linea of texto.split('\n')) {
        const esTitulo = linea.includes('"ai-title"') || linea.includes('"custom-title"');
        if (!esTitulo && !linea.includes('"usage"')) continue;
        let e;
        try { e = JSON.parse(linea); } catch { continue; }
        const idSes = e.sessionId || k;
        if (e.type === 'ai-title' && e.aiTitle) { ses(idSes).tituloAI = e.aiTitle; continue; }
        if (e.type === 'custom-title' && e.customTitle) { ses(idSes).tituloPropio = e.customTitle; continue; }
        const msg = e.message;
        const u = msg && msg.usage;
        if (!u || e.type !== 'assistant' || !msg.model || msg.model === '<synthetic>') continue;
        const ts = new Date(e.timestamp);
        if (isNaN(ts)) continue;
        const s = ses(idSes);
        if (!s.ini || ts < s.ini) s.ini = ts;
        if (!s.fin || ts > s.fin) s.fin = ts;
        const sub = esSub || !!e.isSidechain;
        if (e.cwd) {
          if (!sub && !s.carpeta) s.carpeta = carpetaRelativa(e.cwd);
          else if (!s.cwdSub) s.cwdSub = carpetaRelativa(e.cwd);
        }
        if (!s.entrypoint && e.entrypoint) s.entrypoint = e.entrypoint;
        // Un mismo mensaje puede aparecer en varias líneas: se toma el máximo de cada contador.
        const clave = (msg.id || e.requestId) ? `${msg.id || ''}:${e.requestId || ''}` : e.uuid;
        const cur = {
          dia: diaLocal(ts), modelo: msg.model, sesion: idSes, sub,
          input: u.input_tokens || 0, output: u.output_tokens || 0,
          cache_creation: u.cache_creation_input_tokens || 0, cache_read: u.cache_read_input_tokens || 0,
        };
        const prev = mensajes.get(clave);
        if (!prev) mensajes.set(clave, cur);
        else for (const c of ['input', 'output', 'cache_creation', 'cache_read']) prev[c] = Math.max(prev[c], cur[c]);
      }
    }
  }
  const agg = new Map();
  for (const m of mensajes.values()) {
    const s = ses(m.sesion);
    const um = s.por_modelo[m.modelo] || (s.por_modelo[m.modelo] = { input: 0, output: 0, cache_creation: 0, cache_read: 0 });
    for (const c of ['input', 'output', 'cache_creation', 'cache_read']) um[c] += m[c];
    s.mensajes++;
    if (m.sub) s.mensajes_subagente++;
    if (desdeDia && m.dia < desdeDia) continue;
    const kf = `${m.dia}|${m.modelo}`;
    let a = agg.get(kf);
    if (!a) agg.set(kf, a = { dia: m.dia, modelo: m.modelo, input: 0, output: 0, cache_creation: 0, cache_read: 0, mensajes: 0 });
    for (const c of ['input', 'output', 'cache_creation', 'cache_read']) a[c] += m[c];
    a.mensajes++;
  }
  const lista = [...sesiones.values()].filter(s => s.mensajes > 0).map(s => ({
    id: s.id, inicio: s.ini.toISOString(), fin: s.fin.toISOString(),
    titulo: s.tituloPropio || s.tituloAI || undefined, carpeta: s.carpeta || s.cwdSub || undefined,
    entrypoint: s.entrypoint, por_modelo: s.por_modelo, mensajes: s.mensajes, mensajes_subagente: s.mensajes_subagente,
  })).sort((a, b) => a.inicio.localeCompare(b.inicio));
  return { archivos, filas: [...agg.values()].sort((a, b) => a.dia.localeCompare(b.dia) || a.modelo.localeCompare(b.modelo)), sesiones: lista };
}

// ---------- Envío (curl.exe: usa el almacén de certificados de Windows) ----------
function curlPath() {
  const sys = path.join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'curl.exe');
  return fs.existsSync(sys) ? sys : 'curl';
}

function enviar(cfg, payload) {
  const tmp = path.join(os.tmpdir(), `ct-${process.pid}-${Date.now()}.json`);
  fs.writeFileSync(tmp, JSON.stringify({ p: payload }));
  try {
    const url = cfg.supabaseUrl.replace(/\/+$/, '') + '/rest/v1/rpc/registrar_consumo';
    const r = cp.spawnSync(curlPath(), [
      '-sS', '--ssl-no-revoke', '--max-time', '60', '-X', 'POST', url,
      '-H', `apikey: ${cfg.supabaseKey}`,
      '-H', 'Content-Type: application/json',
      '--data-binary', `@${tmp}`,
      '-w', '\n%{http_code}',
    ], { encoding: 'utf8', windowsHide: true });
    if (r.error) throw new Error(`no se pudo ejecutar curl: ${r.error.message}`);
    const salida = (r.stdout || '').trim().split('\n');
    const code = salida.pop();
    if (code !== '200') throw new Error(`HTTP ${code} ${salida.join(' ').slice(0, 300)} ${(r.stderr || '').trim()}`);
    return salida.join('');
  } finally {
    try { fs.unlinkSync(tmp); } catch { /* ignorar */ }
  }
}

// ---------- Principal ----------
function main() {
  const cfg = leerJson(path.join(DIR, 'config.json'), null);
  if (!cfg || !cfg.supabaseUrl || !cfg.supabaseKey || cfg.supabaseUrl.includes('TU-PROYECTO')) {
    log('ERROR: falta configurar config.json (supabaseUrl y supabaseKey).');
    if (!DRY) process.exit(2);
  }
  // Una PC puede tener varias carpetas de Claude Code (CLAUDE_CONFIG_DIR), cada una con su cuenta.
  // Solo se suman las carpetas cuya sesión es la cuenta compartida.
  const objetivo = ((cfg && cfg.cuentaClaude) || '').toLowerCase();
  const perfiles = perfilesClaude();
  log('carpetas Claude: ' + perfiles.map(p => `${path.basename(p.dir)}=${ocultar(p.cuenta, objetivo)}`).join(' '));
  const carpetas = perfiles.filter(p => DRY || !objetivo || p.cuenta === objetivo).map(p => path.join(p.dir, 'projects'));
  if (!carpetas.length) {
    log(`Ninguna carpeta de Claude Code tiene la sesion de ${objetivo}. No se envia nada.`);
    return;
  }

  const estado = leerJson(ESTADO_FILE, {});
  const completo = ARGS.includes('--completo') || !estado.historialEnviado;
  const dias = (cfg && cfg.diasRecalculo) || 7;
  const desde = completo ? null : diaLocal(new Date(Date.now() - (dias - 1) * 86400000));

  const id = identidad();
  const quien = leerJson(path.join(DIR, 'identidad.json'), {}) || {};
  const decl = /^[a-z0-9._%+-]+@dlwlatam\.com$/i.test(quien.correo || '') ? { nombre: quien.nombre || '', correo: String(quien.correo).toLowerCase() } : { nombre: '', correo: '' };
  const { archivos, filas, sesiones } = recolectar(desde, carpetas);
  log(`maquina=${id.hostname} usuario=${id.usuario} id=${id.machine_id} archivos=${archivos} filas=${filas.length} sesiones=${sesiones.length} desde=${desde || 'inicio'}`);

  if (DRY) {
    console.log(JSON.stringify({ ...id, version: VERSION, filas, sesiones }, null, 2));
    return;
  }

  // Lotes para no exceder el límite de la función: 2000 filas diarias y 300 sesiones por llamada.
  const LF = 2000, LS = 300;
  const total = filas.length;
  for (let n = 0; n === 0 || n * LF < filas.length || n * LS < sesiones.length; n++) {
    enviar(cfg, { ...id, ...decl, version: VERSION, filas: filas.slice(n * LF, (n + 1) * LF), sesiones: sesiones.slice(n * LS, (n + 1) * LS) });
  }
  estado.historialEnviado = true;
  estado.ultimoEnvio = new Date().toISOString();
  fs.writeFileSync(ESTADO_FILE, JSON.stringify(estado, null, 2));
  log(`OK: ${total} filas enviadas.`);
}

try { main(); } catch (err) { log(`ERROR: ${err.message}`); process.exit(1); }
