package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"controls/internal/control"
	"controls/internal/reports"
)

func reportCommand(ctx context.Context, args []string, client *control.Client, out io.Writer) error {
	positional, options, err := parse(args, map[string]option{"--factory": {"factory", true}, "--from": {"from", true}, "--to": {"to", true}, "--snapshot": {"snapshot", true}, "-o": {"format", true}, "--output": {"format", true}})
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return errors.New("usage: factory report [EXECUTION-ID] [--factory NAME] [--from DATE] [--to DATE] [--snapshot ID] [-o html|json|template]")
	}
	// parse preserves repeated options as slices; last selection follows other CLI commands.
	query := reports.Query{Factory: last(options["factory"]), ReportID: last(options["snapshot"])}
	if len(positional) == 1 {
		query.ExecutionID = positional[0]
	}
	for _, field := range []struct {
		name   string
		target **time.Time
	}{{"from", &query.From}, {"to", &query.To}} {
		if text := last(options[field.name]); text != "" {
			value, err := time.Parse(time.RFC3339, text)
			if err != nil {
				value, err = time.Parse("2006-01-02", text)
			}
			if err != nil {
				return fmt.Errorf("%s must be a UTC date or RFC3339 timestamp", field.name)
			}
			*field.target = &value
		}
	}
	format := last(options["format"])
	if format == "" {
		format = "html"
	}
	if format == "template" {
		source, err := client.ReportTemplate(ctx)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, source)
		return err
	}
	if format != "html" && format != "json" {
		return errors.New("report output must be html, json or template")
	}
	document, err := client.Report(ctx, query)
	if err != nil {
		return err
	}
	if format == "json" {
		return printJSON(out, document)
	}
	_, err = io.WriteString(out, document.HTML)
	return err
}
