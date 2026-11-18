-- Gestor de Tokens · Migración 04: equipos (QA, Dev, ...) con su líder.
-- Cada miembro pertenece a un equipo; sus máquinas y sesiones cuentan para ese equipo.
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.

create table if not exists public.equipos (
  id           uuid primary key default gen_random_uuid(),
  nombre       text not null unique,
  lider        text,
  lider_correo text,   -- si coincide con el usuario del panel, el panel abre filtrado en su equipo
  creado       timestamptz not null default now()
);

alter table public.miembros add column if not exists equipo_id uuid references public.equipos(id) on delete set null;

alter table public.equipos enable row level security;
revoke all on public.equipos from anon;
drop policy if exists gestionar_equipos on public.equipos;
create policy gestionar_equipos on public.equipos
  for all to authenticated using (true) with check (true);

insert into public.equipos (nombre, lider) values
  ('QA',  'Liliana'),
  ('Dev', 'Carlos')
on conflict (nombre) do nothing;
