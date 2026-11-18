-- Gestor de Tokens · Migración 06: lectura automática de la cuota del plan.
-- El agente (v2.3.0+) consulta el mismo dato que muestra /usage en Claude Code (% del límite semanal,
-- % de la sesión de 5 h y sus reinicios) en las máquinas con la cuenta compartida, y lo envía aquí.
-- Solo viajan porcentajes y fechas: la credencial de Claude Code nunca sale de la máquina.
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.

create table if not exists public.cuota_lecturas (
  id               bigserial primary key,
  machine_id       text not null references public.maquinas (machine_id) on delete cascade,
  leido            timestamptz not null default now(),
  semanal_pct      numeric(6,2),
  semanal_reinicio timestamptz,
  sesion_pct       numeric(6,2),
  sesion_reinicio  timestamptz,
  detalle          jsonb not null default '{}'::jsonb   -- límites por modelo y desglose Claude Code / chat
);
create index if not exists cuota_lecturas_leido_idx on public.cuota_lecturas (leido desc);

alter table public.cuota_lecturas enable row level security;
drop policy if exists cuota_lecturas_lectura on public.cuota_lecturas;
create policy cuota_lecturas_lectura on public.cuota_lecturas for select to authenticated using (true);
drop policy if exists cuota_lecturas_borrado on public.cuota_lecturas;
create policy cuota_lecturas_borrado on public.cuota_lecturas for delete to authenticated using (true);

create or replace function public.registrar_cuota(p jsonb)
returns integer
language plpgsql
security definer
set search_path = public
as $$
declare
  v_mid text := p->>'machine_id';
  v_sem numeric := (p->>'semanal_pct')::numeric;
  v_ses numeric := (p->>'sesion_pct')::numeric;
begin
  -- Solo máquinas que ya reportan consumo (registrar_consumo las crea).
  if v_mid is null or not exists (select 1 from maquinas where machine_id = v_mid) then
    raise exception 'maquina desconocida';
  end if;
  if v_sem is null or v_sem < 0 or v_sem > 1000 or (v_ses is not null and (v_ses < 0 or v_ses > 1000)) then
    raise exception 'porcentaje invalido';
  end if;
  -- Varias máquinas leen la misma cuenta: basta una lectura cada 10 minutos.
  if exists (select 1 from cuota_lecturas where leido > now() - interval '10 minutes') then
    return 0;
  end if;
  insert into cuota_lecturas (machine_id, semanal_pct, semanal_reinicio, sesion_pct, sesion_reinicio, detalle)
  values (v_mid, v_sem, (p->>'semanal_reinicio')::timestamptz, v_ses, (p->>'sesion_reinicio')::timestamptz,
          case when jsonb_typeof(p->'detalle') = 'object' and length((p->'detalle')::text) < 4000 then p->'detalle' else '{}'::jsonb end);
  -- Se conservan 60 días de lecturas.
  delete from cuota_lecturas where leido < now() - interval '60 days';
  return 1;
end;
$$;

revoke all on function public.registrar_cuota(jsonb) from public;
grant execute on function public.registrar_cuota(jsonb) to anon, authenticated;
