SELECT jsonb_pretty(jsonb_build_object(
 'name', t.name, 'title', t.title, 'submission_format', to_jsonb(t.submission_format),
 'primary_statements', to_jsonb(t.primary_statements), 'allowed_languages', to_jsonb(t.allowed_languages),
 'token_mode', t.token_mode, 'token_max_number', t.token_max_number, 'token_min_interval', extract(epoch from t.token_min_interval),
 'token_gen_initial', t.token_gen_initial, 'token_gen_number', t.token_gen_number, 'token_gen_interval', extract(epoch from t.token_gen_interval), 'token_gen_max', t.token_gen_max,
 'max_submission_number', t.max_submission_number, 'max_user_test_number', t.max_user_test_number,
 'min_submission_interval', extract(epoch from t.min_submission_interval), 'min_user_test_interval', extract(epoch from t.min_user_test_interval),
 'feedback_level', t.feedback_level, 'score_precision', t.score_precision, 'score_mode', t.score_mode,
 'active_dataset', (SELECT description FROM datasets WHERE id = t.active_dataset_id),
 'statements', (SELECT coalesce(jsonb_object_agg(language, digest), '{}') FROM statements WHERE task_id = t.id),
 'attachments', (SELECT coalesce(jsonb_object_agg(filename, digest), '{}') FROM attachments WHERE task_id = t.id),
 'datasets', (SELECT jsonb_agg(jsonb_build_object(
    'description', d.description, 'autojudge', d.autojudge, 'time_limit', d.time_limit, 'memory_limit', d.memory_limit,
    'task_type', d.task_type, 'task_type_parameters', d.task_type_parameters::text,
    'score_type', d.score_type, 'score_type_parameters', d.score_type_parameters::text,
    'managers', (SELECT coalesce(jsonb_object_agg(filename, digest), '{}') FROM managers WHERE dataset_id = d.id),
    'testcases', (SELECT coalesce(jsonb_agg(jsonb_build_array(codename, public, input, output) ORDER BY codename), '[]') FROM testcases WHERE dataset_id = d.id)
  ) ORDER BY d.description) FROM datasets d WHERE d.task_id = t.id)
)) FROM tasks t WHERE t.name = :'task';
