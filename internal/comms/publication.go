package comms

import (
	"context"
	"encoding/json"
	"net/url"

	"controls/execution"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/jackc/pgx/v5"
)

func (s *Service) publish(ctx context.Context, tx pgx.Tx, p Participant, m *Message) (*Effect, error) {
	publisher, ok := s.Collector.(Publisher)
	if !ok {
		return nil, fault("artifact_validation_failed", "runner cannot publish work")
	}
	data, err := decodeData[struct {
		Files []string `json:"files"`
	}](m.Payload)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT work FROM factory.comms_participants WHERE id=$1 AND kind='agent'`, p.ID).Scan(&raw); err != nil {
		return nil, fault("not_authorized", "only assigned workers publish project files")
	}
	var work execution.Work
	if err = json.Unmarshal(raw, &work); err != nil {
		return nil, err
	}
	if m.ThreadID != threadID(work) {
		return nil, fault("not_authorized", "publish work in the assigned control thread")
	}
	files, err := publisher.Publish(ctx, work, data.Files, m.ID)
	if err != nil {
		return nil, fault("artifact_validation_failed", "%v", err)
	}
	artifacts := make([]execution.Artifact, 0, len(files))
	parts := a2a.ContentParts{}
	for _, file := range files {
		a := file.Artifact
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_publications(message_id,name,uri,digest,body) VALUES($1,$2,$3,$4,$5)`, m.ID, a.Name, a.URI, a.Digest, file.Content)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
		parts = append(parts, a2a.FilePart{File: a2a.FileURI{URI: (&url.URL{Scheme: "file", Path: a.URI}).String(), FileMeta: a2a.FileMeta{Name: a.Name}}, Metadata: map[string]any{"sha256": a.Digest}})
	}
	// Replace caller data with the verified manifest; never trust claimed URIs/digests.
	m.Payload.Parts = append(a2a.ContentParts{a2a.DataPart{Data: map[string]any{"publication_id": m.ID, "artifacts": artifacts}}}, parts...)
	_, err = tx.Exec(ctx, `UPDATE factory.comms_threads SET publication_id=$2 WHERE id=$1`, m.ThreadID, m.ID)
	return &Effect{State: "accepted", ResultID: m.ID}, err
}
func (s *Service) checkPublication(ctx context.Context, tx pgx.Tx, work execution.Work, result execution.StepResult, thread string) error {
	var latest string
	if err := tx.QueryRow(ctx, `SELECT publication_id FROM factory.comms_threads WHERE id=$1`, thread).Scan(&latest); err != nil {
		return err
	}
	if latest == "" {
		if result.PublicationID != "" {
			return fault("stale_revision", "publication does not belong to this thread")
		}
		return nil
	}
	// A failure outcome may explain a blocker without claiming acceptance of a revision.
	if result.PublicationID == "" && work.Step.Routes[result.Outcome] == execution.Fail {
		return nil
	}
	if result.PublicationID != latest {
		return fault("stale_revision", "result must refer to current publication %s", latest)
	}
	rows, err := tx.Query(ctx, `SELECT name,digest FROM factory.comms_publications WHERE message_id=$1`, latest)
	if err != nil {
		return err
	}
	names := []string{}
	digests := map[string]string{}
	for rows.Next() {
		var name, digest string
		if err = rows.Scan(&name, &digest); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
		digests[name] = digest
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if s.Collector == nil {
		return fault("artifact_validation_failed", "runner cannot verify publication")
	}
	check := work
	check.Step.Outputs = nil
	current, err := s.Collector.Collect(ctx, check, names)
	if err != nil {
		return fault("stale_revision", "published source changed: %v", err)
	}
	for _, artifact := range current {
		if artifact.Digest != digests[artifact.Name] {
			return fault("stale_revision", "%s changed after publication; publish and review the new revision", artifact.Name)
		}
	}
	return nil
}
