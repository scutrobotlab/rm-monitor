\set ON_ERROR_STOP on

\if :{?profile}
\else
\set profile baseline
\endif

create temporary table pressure_config(profile text primary key, match_count int, rounds_per_match int, roles_per_round int);
insert into pressure_config values
  ('smoke', 50, 3, 3),
  ('baseline', 200, 3, 4),
  ('stress', 2000, 3, 4),
  ('hot', 10000, 3, 4);

create temporary table pressure_params as
select * from pressure_config where profile = :'profile';

do $$
begin
  if not exists (select 1 from pressure_params) then
    raise exception 'unknown pressure profile';
  end if;
end $$;

insert into teams(id, name, school_name, school_logo, created_at, updated_at)
select 'pressure-team-' || g,
       'Team ' || g,
       'School ' || g,
       '',
       now() - interval '1 day',
       now() - interval '1 day'
from generate_series(1, (select match_count * 2 from pressure_params)) g
on conflict (id) do nothing;

insert into matches(id, event, zone, "order", match_type, match_slug, total_rounds, priority, result, winner_placeholder_name, loser_placeholder_name, latest_status, report, created_at, updated_at, team_red_matches, team_blue_matches)
select 'pressure-match-' || g,
       'Pressure Event',
       'Pressure Zone ' || (g % 8),
       g,
       'BO3',
       'pressure-match-' || g,
       3,
       g % 10,
       'UNKNOWN',
       '',
       '',
       'DONE',
       'pressure-seeded',
       now() - interval '1 day',
       now() - interval '1 day',
       'pressure-team-' || (g * 2 - 1),
       'pressure-team-' || (g * 2)
from generate_series(1, (select match_count from pressure_params)) g
on conflict (id) do nothing;

insert into match_rounds(round_no, status, winner, started_at, ended_at, created_at, updated_at, match_rounds)
select r.round_no,
       'ENDED',
       case when r.round_no % 2 = 0 then 'blue' else 'red' end,
       now() - interval '1 day',
       now() - interval '1 day' + interval '10 minutes',
       now() - interval '1 day',
       now() - interval '1 day',
       'pressure-match-' || m.match_no
from generate_series(1, (select match_count from pressure_params)) m(match_no)
cross join generate_series(1, (select rounds_per_match from pressure_params)) r(round_no)
on conflict do nothing;

insert into record_tasks(role, source_url, output_path, status, k8s_job_name, attempts, priority, file_size, checksum, error_message, started_at, completed_at, created_at, updated_at, match_round_record_tasks)
select 'role-' || role_no,
       'http://pressure.invalid/' || mr.id || '/' || role_no || '.m3u8',
       'Pressure/match-' || mr.match_rounds || '/Round-' || mr.round_no || '/role-' || role_no || '.flv',
       case when role_no % 2 = 0 then 'SUCCEEDED' else 'FAILED' end,
       null,
       role_no % 3,
       role_no % 10,
       1024,
       'checksum-' || mr.id || '-' || role_no,
       null,
       now() - interval '23 hours',
       now() - interval '22 hours',
       now() - interval '1 day',
       now() - interval '1 day',
       mr.id
from match_rounds mr
cross join generate_series(1, (select roles_per_round from pressure_params)) role_no
where mr.match_rounds like 'pressure-match-%'
on conflict do nothing;

insert into media_artifacts(kind, path, format, codec, file_size, checksum, status, deletable_at, deleted_at, created_at, updated_at, record_task_media_artifacts)
select 'source',
       rt.output_path,
       'flv',
       'copy',
       rt.file_size,
       rt.checksum,
       'AVAILABLE',
       case when rt.id % 5 = 0 then now() - interval '1 hour' else null end,
       null,
       now() - interval '1 day',
       now() - interval '1 day',
       rt.id
from record_tasks rt
where rt.output_path like 'Pressure/%'
on conflict do nothing;

insert into upload_tasks(source_path, status, k8s_job_name, attempts, priority, bitable_app_token, bitable_table_id, bitable_record_id, bitable_record_url, attachment_file_token, error_message, started_at, completed_at, created_at, updated_at, media_artifact_upload_task, record_task_upload_task)
select ma.path,
       case when ma.id % 3 = 0 then 'RUNNING' else 'SUCCEEDED' end,
       null,
       ma.id % 4,
       ma.id % 10,
       'app-token',
       'table-id',
       'record-' || ma.id,
       'https://example.invalid/record-' || ma.id,
       'file-' || ma.id,
       null,
       now() - interval '23 hours',
       case when ma.id % 3 = 0 then null else now() - interval '22 hours' end,
       now() - interval '1 day',
       now() - interval '1 day',
       ma.id,
       ma.record_task_media_artifacts
from media_artifacts ma
where ma.path like 'Pressure/%'
on conflict do nothing;

insert into transcode_tasks(status, k8s_job_name, attempts, priority, error_message, started_at, completed_at, created_at, updated_at, media_artifact_source_transcode_task, media_artifact_archive_transcode_task)
select case when ma.id % 4 = 0 then 'RUNNING' else 'SUCCEEDED' end,
       null,
       ma.id % 3,
       ma.id % 10,
       null,
       now() - interval '23 hours',
       case when ma.id % 4 = 0 then null else now() - interval '22 hours' end,
       now() - interval '1 day',
       now() - interval '1 day',
       ma.id,
       null
from media_artifacts ma
where ma.path like 'Pressure/%'
on conflict do nothing;

insert into ocr_tasks(role, status, priority, k8s_job_name, attempts, settlement_path, settlement_json, error_message, started_at, completed_at, created_at, updated_at, match_round_ocr_tasks, media_artifact_ocr_tasks)
select 'role-1',
       case when ma.id % 5 = 0 then 'RUNNING' else 'SUCCEEDED' end,
       ma.id % 10,
       null,
       ma.id % 2,
       'settlement-' || ma.id,
       '{}',
       null,
       now() - interval '23 hours',
       case when ma.id % 5 = 0 then null else now() - interval '22 hours' end,
       now() - interval '1 day',
       now() - interval '1 day',
       rt.match_round_record_tasks,
       ma.id
from media_artifacts ma
join record_tasks rt on rt.id = ma.record_task_media_artifacts
where ma.path like 'Pressure/%'
on conflict do nothing;

insert into highlight_clips(highlight_index, role, algorithm_version, status, priority, k8s_job_name, attempts, start_seconds, end_seconds, peak_seconds, output_dir, title, description, tags, score, model_payload, error_message, started_at, completed_at, created_at, updated_at, match_round_highlight_clips, media_artifact_highlight_clips)
select 1,
       'role-1',
       'pressure-v1',
       case when ma.id % 6 = 0 then 'RUNNING' else 'SUCCEEDED' end,
       ma.id % 10,
       null,
       ma.id % 3,
       10,
       40,
       25,
       'Pressure/highlight-' || ma.id,
       'title',
       'description',
       '[]'::jsonb,
       1.0,
       '{}',
       null,
       now() - interval '23 hours',
       case when ma.id % 6 = 0 then null else now() - interval '22 hours' end,
       now() - interval '1 day',
       now() - interval '1 day',
       rt.match_round_record_tasks,
       ma.id
from media_artifacts ma
join record_tasks rt on rt.id = ma.record_task_media_artifacts
where ma.path like 'Pressure/%'
on conflict do nothing;

insert into highlight_publish_tasks(platform, status, priority, k8s_job_name, attempts, publish_url, external_id, error_message, started_at, completed_at, created_at, updated_at, highlight_clip_publish_tasks)
select 'bilibili',
       case when id % 7 = 0 then 'RUNNING' else 'SUCCEEDED' end,
       id % 10,
       null,
       id % 3,
       'https://example.invalid/video-' || id,
       'external-' || id,
       null,
       now() - interval '23 hours',
       case when id % 7 = 0 then null else now() - interval '22 hours' end,
       now() - interval '1 day',
       now() - interval '1 day',
       id
from highlight_clips
where output_dir like 'Pressure/%'
on conflict do nothing;

analyze;

select 'seeded' as phase, :'profile' as profile,
  (select count(*) from matches) as matches,
  (select count(*) from match_rounds) as rounds,
  (select count(*) from record_tasks) as record_tasks,
  (select count(*) from media_artifacts) as media_artifacts,
  (select count(*) from upload_tasks) as upload_tasks,
  (select count(*) from transcode_tasks) as transcode_tasks,
  (select count(*) from ocr_tasks) as ocr_tasks,
  (select count(*) from highlight_clips) as highlight_clips,
  (select count(*) from highlight_publish_tasks) as highlight_publish_tasks;
