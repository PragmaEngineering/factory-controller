CREATE TABLE factory.comms_participants (
 id text PRIMARY KEY, execution_id text NOT NULL, name text NOT NULL, kind text NOT NULL,
 token_hash text UNIQUE, active boolean NOT NULL DEFAULT true,
 work jsonb, result jsonb, session_id text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE factory.comms_threads (
 id text PRIMARY KEY, execution_id text NOT NULL, title text NOT NULL, context jsonb NOT NULL, owner_id text NOT NULL,
 closed boolean NOT NULL DEFAULT false, next_sequence bigint NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE factory.comms_members (
 thread_id text REFERENCES factory.comms_threads(id), participant_id text REFERENCES factory.comms_participants(id),
 joined_sequence bigint NOT NULL, removed boolean NOT NULL DEFAULT false, ack_sequence bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(thread_id,participant_id)
);
CREATE TABLE factory.comms_messages (
 id text PRIMARY KEY, thread_id text NOT NULL REFERENCES factory.comms_threads(id), sequence bigint NOT NULL,
 sender text NOT NULL REFERENCES factory.comms_participants(id), audience text[] NOT NULL,
 in_reply_to text NOT NULL DEFAULT '', payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(thread_id,sequence)
);
CREATE TABLE factory.comms_receipts (
 participant_id text NOT NULL REFERENCES factory.comms_participants(id), operation_id text NOT NULL,
 fingerprint text NOT NULL, receipt jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(participant_id,operation_id)
);
CREATE TABLE factory.comms_requests (
 id text PRIMARY KEY REFERENCES factory.comms_messages(id) DEFERRABLE INITIALLY DEFERRED, execution_id text NOT NULL,
 thread_id text NOT NULL REFERENCES factory.comms_threads(id), owner text NOT NULL REFERENCES factory.comms_participants(id),
 prompt text NOT NULL, options text[] NOT NULL, state text NOT NULL DEFAULT 'pending',
 response jsonb, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON factory.comms_threads(execution_id);
CREATE INDEX ON factory.comms_requests(execution_id,state);
