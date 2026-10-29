CREATE TABLE factory.activity (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execution_id text NOT NULL,
    session_id text NOT NULL DEFAULT '',
    source text NOT NULL,
    data bytea NOT NULL,
    at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_execution_id_id ON factory.activity (execution_id, id);
