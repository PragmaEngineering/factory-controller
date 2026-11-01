CREATE TABLE factory.comms_completion (
 participant_id text PRIMARY KEY REFERENCES factory.comms_participants(id),
 runtime_state text NOT NULL DEFAULT '' CHECK (runtime_state IN ('','busy','idle')),
 state_since timestamptz,
 rejection text NOT NULL DEFAULT '',
 reminders integer NOT NULL DEFAULT 0 CHECK (reminders >= 0),
 last_reminded_at timestamptz,
 blocked_reason text NOT NULL DEFAULT ''
);
