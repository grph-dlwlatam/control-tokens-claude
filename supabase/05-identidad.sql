-- Gestor de Tokens · Migración 05: identidad declarada en el agente (nombre completo + correo @dlwlatam.com).
-- Con el correo, la máquina se vincula sola a su miembro (o se crea el miembro si no existe).
-- La asignación manual de la líder siempre manda: solo se vinculan máquinas que aún no tienen miembro.
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.

alter table public.maquinas add column if not exists nombre_declarado text;
alter table public.maquinas add column if not exists correo_declarado text;
create index if not exists miembros_correo_idx on public.miembros (lower(correo));

create or replace function public.registrar_consumo(p jsonb)
returns integer
language plpgsql
security definer
set search_path = public
as $$
declare
  v_mid    text := p->>'machine_id';
  v_filas  jsonb := coalesce(p->'filas', '[]'::jsonb);
  v_ses    jsonb := coalesce(p->'sesiones', '[]'::jsonb);
  v_nombre text := nullif(left(trim(coalesce(p->>'nombre', '')), 120), '');
  v_correo text := nullif(lower(left(trim(coalesce(p->>'correo', '')), 160)), '');
  v_mb     uuid;
  n        integer;
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
  -- Solo se aceptan correos corporativos.
  if v_correo is not null and v_correo !~ '^[a-z0-9._%+-]+@dlwlatam\.com$' then
    v_correo := null;
  end if;

  insert into maquinas (machine_id, hostname, usuario_windows, version_agente, ultimo_reporte, nombre_declarado, correo_declarado)
  values (v_mid, left(p->>'hostname', 100), left(p->>'usuario', 100), left(p->>'version', 20), now(), v_nombre, v_correo)
  on conflict (machine_id) do update
    set hostname         = excluded.hostname,
        usuario_windows  = excluded.usuario_windows,
        version_agente   = excluded.version_agente,
        ultimo_reporte   = now(),
        nombre_declarado = coalesce(excluded.nombre_declarado, maquinas.nombre_declarado),
        correo_declarado = coalesce(excluded.correo_declarado, maquinas.correo_declarado);

  -- Vinculación automática por correo (no pisa una asignación manual).
  if v_correo is not null then
    select id into v_mb from miembros where lower(correo) = v_correo order by creado limit 1;
    if v_mb is null then
      insert into miembros (nombre, correo)
      values (coalesce(v_nombre, split_part(v_correo, '@', 1)), v_correo)
      returning id into v_mb;
    end if;
    update maquinas set miembro_id = v_mb where machine_id = v_mid and miembro_id is null;
  end if;

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
