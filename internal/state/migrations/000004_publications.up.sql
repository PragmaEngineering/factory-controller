ALTER TABLE factory.comms_threads ADD COLUMN publication_id text NOT NULL DEFAULT '';
CREATE TABLE factory.comms_publications (
 message_id text NOT NULL REFERENCES factory.comms_messages(id) DEFERRABLE INITIALLY DEFERRED,
 name text NOT NULL, uri text NOT NULL, digest text NOT NULL, body bytea NOT NULL,
 PRIMARY KEY(message_id,name)
);
