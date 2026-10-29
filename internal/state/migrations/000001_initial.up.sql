CREATE SCHEMA IF NOT EXISTS factory;

CREATE TABLE IF NOT EXISTS factory.records (
    kind text NOT NULL,
    id text NOT NULL,
    content jsonb NOT NULL,
    PRIMARY KEY (kind, id)
);

CREATE TABLE IF NOT EXISTS factory.output (
    session_id text NOT NULL,
    position bigint GENERATED ALWAYS AS IDENTITY,
    body bytea NOT NULL,
    PRIMARY KEY (session_id, position)
);
