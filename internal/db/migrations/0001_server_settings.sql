-- Server-wide settings edited from the administration "Server" page: the
-- timezone of the whole installation, the public addresses of the three
-- sites (":PORT" means "same host as the current page"), ... One row per
-- key; values are JSON so that adding a setting needs no migration.
CREATE TABLE server_settings (
    key        text PRIMARY KEY CHECK (key ~ '^[a-z][a-z0-9_.]*$'),
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
