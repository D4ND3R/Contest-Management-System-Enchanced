-- Identifiers (contest/task/group names, usernames, team codes, testcase
-- codenames, file names, statement languages, setting keys) compare and sort
-- by code point, as CMS does in Python, whatever the locale the database was
-- created with: a linguistic collation such as en_US ignores punctuation and
-- would sort "aaa.h" before "aaa.%l" or make listings differ between
-- installations. Unique constraints on these columns are rebuilt with the
-- new collation.
ALTER TABLE contests              ALTER COLUMN name     TYPE text COLLATE "C";
ALTER TABLE groups                ALTER COLUMN name     TYPE text COLLATE "C";
ALTER TABLE tasks                 ALTER COLUMN name     TYPE text COLLATE "C";
ALTER TABLE users                 ALTER COLUMN username TYPE text COLLATE "C";
ALTER TABLE admins                ALTER COLUMN username TYPE text COLLATE "C";
ALTER TABLE teams                 ALTER COLUMN code     TYPE text COLLATE "C";
ALTER TABLE testcases             ALTER COLUMN codename TYPE text COLLATE "C";
ALTER TABLE statements            ALTER COLUMN language TYPE text COLLATE "C";
ALTER TABLE attachments           ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE managers              ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE files                 ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE executables           ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE user_test_files       ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE user_test_managers    ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE user_test_executables ALTER COLUMN filename TYPE text COLLATE "C";
ALTER TABLE server_settings       ALTER COLUMN key      TYPE text COLLATE "C";
