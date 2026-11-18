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

const VERSION = '1.0.0';
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

function listarJsonl(dir, desdeMs, out) {
  let entradas;
  try { entradas = fs.readdirSync(dir, { withFileTypes: true }); } catch { return out; }
  for (const e of entradas) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) listarJsonl(p, desdeMs, out);
    else if (e.isFile() && e.name.endsWith('.jsonl')) {
      try { if (fs.statSync(p).mtimeMs >= desdeMs) out.push(p); } catch { /* ignorar */ }
    }
  }
  return out;
}

function diaLocal(d) {
  const z = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${z(d.getMonth() + 1)}-${z(d.getDate())}`;
}

function recolectar(desdeDia, carpetas) {
  const desdeMs = desdeDia ? new Date(`${desdeDia}T00:00:00`).getTime() : 0;
  const archivos = carpetas.flatMap(d => listarJsonl(d, desdeMs, []));
  // Un mismo mensaje puede aparecer en varias líneas (y en archivos de subagentes):
  // se deduplica por message.id + requestId quedándose con el máximo de cada contador.
  const mensajes = new Map();
  for (const file of archivos) {
    let texto;
    try { texto = fs.readFileSync(file, 'utf8'); } catch { continue; }
    for (const linea of texto.split('\n')) {
      if (!linea.includes('"usage"')) continue;
      let e;
      try { e = JSON.parse(linea); } catch { continue; }
      const msg = e && e.message;
      const u = msg && msg.usage;
      if (!u || e.type !== 'assistant' || !msg.model || msg.model === '<synthetic>') continue;
      const ts = new Date(e.timestamp);
      if (isNaN(ts)) continue;
      const dia = diaLocal(ts);
      if (desdeDia && dia < desdeDia) continue;
      const clave = (msg.id || e.requestId) ? `${msg.id || ''}:${e.requestId || ''}` : e.uuid;
      const prev = mensajes.get(clave);
      const cur = {
        dia, modelo: msg.model,
        input: u.input_tokens || 0,
        output: u.output_tokens || 0,
        cache_creation: u.cache_creation_input_tokens || 0,
        cache_read: u.cache_read_input_tokens || 0,
      };
      if (!prev) mensajes.set(clave, cur);
      else for (const k of ['input', 'output', 'cache_creation', 'cache_read']) prev[k] = Math.max(prev[k], cur[k]);
    }
  }
  const agg = new Map();
  for (const m of mensajes.values()) {
    const k = `${m.dia}|${m.modelo}`;
    let a = agg.get(k);
    if (!a) agg.set(k, a = { dia: m.dia, modelo: m.modelo, input: 0, output: 0, cache_creation: 0, cache_read: 0, mensajes: 0 });
    a.input += m.input; a.output += m.output; a.cache_creation += m.cache_creation; a.cache_read += m.cache_read; a.mensajes++;
  }
  return { archivos: archivos.length, filas: [...agg.values()].sort((a, b) => a.dia.localeCompare(b.dia)) };
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
  const { archivos, filas } = recolectar(desde, carpetas);
  log(`maquina=${id.hostname} usuario=${id.usuario} id=${id.machine_id} archivos=${archivos} filas=${filas.length} desde=${desde || 'inicio'}`);

  if (DRY) {
    console.log(JSON.stringify({ ...id, version: VERSION, filas }, null, 2));
    return;
  }

  // Lotes para no exceder el límite de la función.
  const LOTE = 2000;
  let total = 0;
  for (let i = 0; i < Math.max(filas.length, 1); i += LOTE) {
    enviar(cfg, { ...id, version: VERSION, filas: filas.slice(i, i + LOTE) });
    total += filas.slice(i, i + LOTE).length;
  }
  estado.historialEnviado = true;
  estado.ultimoEnvio = new Date().toISOString();
  fs.writeFileSync(ESTADO_FILE, JSON.stringify(estado, null, 2));
  log(`OK: ${total} filas enviadas.`);
}

try { main(); } catch (err) { log(`ERROR: ${err.message}`); process.exit(1); }
