-- Control de consumo de Claude Code por máquina
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.

-- ============ Tablas ============
create table if not exists public.maquinas (
  machine_id      text primary key,
  hostname        text,
  usuario_windows text,
  persona         text,              -- nombre que asigna la líder desde el dashboard
  version_agente  text,
  primera_vez     timestamptz not null default now(),
  ultimo_reporte  timestamptz
);

create table if not exists public.consumo_diario (
  machine_id            text not null references public.maquinas(machine_id) on delete cascade,
  dia                   date not null,
  modelo                text not null,
  input_tokens          bigint not null default 0,
  output_tokens         bigint not null default 0,
  cache_creation_tokens bigint not null default 0,
  cache_read_tokens     bigint not null default 0,
  mensajes              integer not null default 0,
  actualizado           timestamptz not null default now(),
  primary key (machine_id, dia, modelo)
);

create index if not exists consumo_diario_dia_idx on public.consumo_diario (dia);

-- ============ Seguridad ============
-- El agente (clave pública / anon) NO toca las tablas: solo puede llamar a registrar_consumo().
-- La líder (usuario autenticado) puede leer todo y asignar nombres.
alter table public.maquinas       enable row level security;
alter table public.consumo_diario enable row level security;

revoke all on public.maquinas, public.consumo_diario from anon;

drop policy if exists lectura_maquinas on public.maquinas;
create policy lectura_maquinas on public.maquinas
  for select to authenticated using (true);

drop policy if exists editar_persona on public.maquinas;
create policy editar_persona on public.maquinas
  for update to authenticated using (true) with check (true);

drop policy if exists lectura_consumo on public.consumo_diario;
create policy lectura_consumo on public.consumo_diario
  for select to authenticated using (true);

-- ============ Función que llama el agente ============
-- Recibe el consumo diario recalculado de una máquina y lo reemplaza (idempotente).
create or replace function public.registrar_consumo(p jsonb)
returns integer
language plpgsql
security definer
set search_path = public
as $$
declare
  v_mid text := p->>'machine_id';
  v_filas jsonb := coalesce(p->'filas', '[]'::jsonb);
  n integer;
begin
  if v_mid is null or length(v_mid) < 8 or length(v_mid) > 64 then
    raise exception 'machine_id invalido';
  end if;
  if jsonb_typeof(v_filas) <> 'array' or jsonb_array_length(v_filas) > 5000 then
    raise exception 'filas invalidas';
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
  return n;
end;
$$;

revoke all on function public.registrar_consumo(jsonb) from public;
grant execute on function public.registrar_consumo(jsonb) to anon, authenticated;
