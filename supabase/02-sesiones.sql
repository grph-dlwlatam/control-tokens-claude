-- Gestor de Tokens · Migración 02: miembros del equipo y detalle por sesión (uso personal).
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.
-- Requiere haber ejecutado antes esquema.sql.

-- ============ Miembros del equipo ============
-- Cada miembro es único y puede tener varias máquinas con la cuenta de Claude Code de Delaware.
create table if not exists public.miembros (
  id      uuid primary key default gen_random_uuid(),
  nombre  text not null,
  correo  text,
  activo  boolean not null default true,
  creado  timestamptz not null default now()
);

alter table public.maquinas add column if not exists miembro_id uuid references public.miembros(id) on delete set null;

-- Migra los nombres escritos a mano en "persona" a miembros (uno por nombre distinto).
insert into public.miembros (nombre)
select distinct trim(persona) from public.maquinas
where coalesce(trim(persona), '') <> ''
  and not exists (select 1 from public.miembros m where lower(m.nombre) = lower(trim(maquinas.persona)));
update public.maquinas q set miembro_id = m.id
from public.miembros m
where q.miembro_id is null and lower(m.nombre) = lower(trim(q.persona));

alter table public.miembros enable row level security;
revoke all on public.miembros from anon;
drop policy if exists gestionar_miembros on public.miembros;
create policy gestionar_miembros on public.miembros
  for all to authenticated using (true) with check (true);

-- ============ Sesiones ============

create table if not exists public.sesiones (
  machine_id          text not null references public.maquinas(machine_id) on delete cascade,
  session_id          text not null,
  inicio              timestamptz,
  fin                 timestamptz,
  titulo              text,          -- título que Claude Code genera para la sesión
  carpeta             text,          -- carpeta de trabajo relativa al perfil del usuario
  entrypoint          text,          -- claude-vscode, cli, ...
  por_modelo          jsonb not null default '{}'::jsonb,  -- {modelo: {input, output, cache_creation, cache_read}}
  mensajes            integer not null default 0,
  mensajes_subagente  integer not null default 0,
  revision            text check (revision in ('trabajo', 'personal')),  -- la asigna la líder
  revisado_por        text,
  revisado_en         timestamptz,
  actualizado         timestamptz not null default now(),
  primary key (machine_id, session_id)
);

create index if not exists sesiones_inicio_idx on public.sesiones (inicio);

alter table public.sesiones enable row level security;
revoke all on public.sesiones from anon;

drop policy if exists lectura_sesiones on public.sesiones;
create policy lectura_sesiones on public.sesiones
  for select to authenticated using (true);

drop policy if exists revisar_sesiones on public.sesiones;
create policy revisar_sesiones on public.sesiones
  for update to authenticated using (true) with check (true);

-- La función del agente ahora también recibe "sesiones" (opcional, compatible con agentes anteriores).
create or replace function public.registrar_consumo(p jsonb)
returns integer
language plpgsql
security definer
set search_path = public
as $$
declare
  v_mid text := p->>'machine_id';
  v_filas jsonb := coalesce(p->'filas', '[]'::jsonb);
  v_ses jsonb := coalesce(p->'sesiones', '[]'::jsonb);
  n integer;
begin
  if v_mid is null or length(v_mid) < 8 or length(v_mid) > 64 then
    raise exception 'machine_id invalido';
  end if;
  if jsonb_typeof(v_filas) <> 'array' or jsonb_array_length(v_filas) > 5000 then
    raise exception 'filas invalidas';
  end if;
  if jsonb_typeof(v_ses) <> 'array' or jsonb_array_length(v_ses) > 2000 then
    raise exception 'sesiones invalidas';
  end if;

  insert into maquinas (machine_id, hostname, usuario_windows, version_agente, ultimo_reporte)
  values (v_mid, left(p->>'hostname', 100), left(p->>'usuario', 100), left(p->>'version', 20), now())
  on conflict (machine_id) do update
    set hostname        = excluded.hostname,
        usuario_windows = excluded.usuario_windows,
        version_agente  = excluded.version_agente,
        ultimo_reporte  = now();

  insert into consumo_diario (machine_id, dia, modelo, input_tokens, output_tokens,
                              cache_creation_tokens, cache_read_tokens, mensajes, actualizado)
  select v_mid,
         (f->>'dia')::date,
         left(f->>'modelo', 80),
         greatest(coalesce((f->>'input')::bigint, 0), 0),
         greatest(coalesce((f->>'output')::bigint, 0), 0),
         greatest(coalesce((f->>'cache_creation')::bigint, 0), 0),
         greatest(coalesce((f->>'cache_read')::bigint, 0), 0),
         greatest(coalesce((f->>'mensajes')::int, 0), 0),
         now()
  from jsonb_array_elements(v_filas) f
  on conflict (machine_id, dia, modelo) do update
    set input_tokens          = excluded.input_tokens,
        output_tokens         = excluded.output_tokens,
        cache_creation_tokens = excluded.cache_creation_tokens,
        cache_read_tokens     = excluded.cache_read_tokens,
        mensajes              = excluded.mensajes,
        actualizado           = now();
  get diagnostics n = row_count;

  -- La revisión de la líder (revision, revisado_por, revisado_en) nunca la toca el agente.
  insert into sesiones (machine_id, session_id, inicio, fin, titulo, carpeta, entrypoint,
                        por_modelo, mensajes, mensajes_subagente, actualizado)
  select v_mid,
         left(s->>'id', 64),
         (s->>'inicio')::timestamptz,
         (s->>'fin')::timestamptz,
         left(s->>'titulo', 200),
         left(s->>'carpeta', 200),
         left(s->>'entrypoint', 40),
         coalesce(s->'por_modelo', '{}'::jsonb),
         greatest(coalesce((s->>'mensajes')::int, 0), 0),
         greatest(coalesce((s->>'mensajes_subagente')::int, 0), 0),
         now()
  from jsonb_array_elements(v_ses) s
  where coalesce(s->>'id', '') <> ''
  on conflict (machine_id, session_id) do update
    set inicio             = excluded.inicio,
        fin                = excluded.fin,
        titulo             = coalesce(excluded.titulo, sesiones.titulo),
        carpeta            = coalesce(excluded.carpeta, sesiones.carpeta),
        entrypoint         = coalesce(excluded.entrypoint, sesiones.entrypoint),
        por_modelo         = excluded.por_modelo,
        mensajes           = excluded.mensajes,
        mensajes_subagente = excluded.mensajes_subagente,
        actualizado        = now();

  return n;
end;
$$;

revoke all on function public.registrar_consumo(jsonb) from public;
grant execute on function public.registrar_consumo(jsonb) to anon, authenticated;
