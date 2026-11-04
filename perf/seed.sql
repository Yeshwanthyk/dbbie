-- Perf fixture: a 1M-row, 24-column table plus 200 small tables (see perf/README.md).
drop table if exists events;
create table events as
select g as id,
  now() - (g || ' seconds')::interval as created_at,
  now() - (g/2 || ' seconds')::interval as updated_at,
  md5(g::text) as hash,
  'user_' || (g % 50000) as username,
  'user' || (g % 50000) || '@example.com' as email,
  (g % 7) as status,
  round((random()*10000)::numeric, 2) as amount,
  repeat('lorem ipsum dolor sit amet ', 1 + g % 6) as description,
  jsonb_build_object('k', g, 'tags', jsonb_build_array('a','b', g % 9), 'nested', jsonb_build_object('x', g*2)) as payload,
  (g % 2 = 0) as flag,
  gen_random_uuid() as uuid,
  case when g % 10 = 0 then null else 'region-' || (g % 12) end as region,
  g * 3.14159 as score,
  'col_e_' || g as e1, 'col_f_' || g as f1, 'col_g_' || g as g1, 'col_h_' || g as h1,
  'col_i_' || g as i1, 'col_j_' || g as j1, 'col_k_' || g as k1, 'col_l_' || g as l1,
  'col_m_' || g as m1, 'col_n_' || g as n1
from generate_series(1, 1000000) g;
alter table events add primary key (id);
analyze events;
do $$ begin for i in 1..200 loop
  execute format('create table if not exists small_%s (id int primary key, name text, created_at timestamptz default now())', i);
end loop; end $$;
