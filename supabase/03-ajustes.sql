-- Gestor de Tokens · Migración 03: ajustes compartidos del panel
-- (palabras clave de uso personal y carpetas de trabajo conocidas).
-- Ejecutar una sola vez en Supabase: SQL Editor > New query > pegar todo > Run.

create table if not exists public.ajustes (
  clave       text primary key,
  valor       jsonb not null,
  actualizado timestamptz not null default now()
);

alter table public.ajustes enable row level security;
revoke all on public.ajustes from anon;
drop policy if exists gestionar_ajustes on public.ajustes;
create policy gestionar_ajustes on public.ajustes
  for all to authenticated using (true) with check (true);

insert into public.ajustes (clave, valor) values
  ('palabras_personales', '["viaje","vacaciones","hotel","vuelo","cv","curriculum","carta de presentacion","universidad","tarea","examen","tesis","colegio","serie","pelicula","netflix","receta","dieta","gimnasio","cumpleanos","regalo","boda","juego","videojuego","apuesta","bitcoin","cripto","horoscopo","futbol","travel","vacation","resume","homework","recipe","movie"]'::jsonb),
  ('carpetas_trabajo', '[]'::jsonb)
on conflict (clave) do nothing;
