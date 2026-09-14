package localexec

import (
	"context"
	"encoding/json"
	"io"
	"time"

	clioutput "github.com/sir-hassan/inframill/internal/cli/output"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func history(ctx context.Context, e *inframill.Engine, id inframill.InstanceID, o options, stdout io.Writer) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if o.commandID != "" {
		c, err := e.GetCommandExecution(ctx, id, inframill.CommandID(o.commandID))
		if err != nil {
			return err
		}
		if o.output == clioutput.JSON {
			return encoder.Encode(commandResult(id, c))
		}
		return writeHistoryCommand(stdout, id, c)
	}
	page, err := e.ListCommandExecutions(ctx, id, inframill.CommandExecutionQuery{Limit: o.limit, Cursor: o.cursor})
	if err != nil {
		return err
	}
	if o.output != clioutput.JSON {
		v := clioutput.View{Headers: []string{"Command UUID", "Command", "Status", "Created at", "Started at", "Ended at"}, Empty: "No commands found.", NextCursor: page.NextCursor, ObservedAt: historyTime(&page.ObservedAt)}
		for _, c := range page.Executions {
			v.Rows = append(v.Rows, []string{string(c.Request.ID), c.Request.Command, string(c.Status), historyTime(&c.CreatedAt), historyTime(c.StartedAt), historyTime(c.EndedAt)})
		}
		return v.Write(stdout)
	}
	results := make([]result, 0, len(page.Executions))
	for _, c := range page.Executions {
		results = append(results, commandResult(id, c))
	}
	return encoder.Encode(struct {
		Executions []result `json:"executions"`
		NextCursor string   `json:"next_cursor,omitempty"`
	}{Executions: results, NextCursor: page.NextCursor})
}

func historyTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func writeHistoryCommand(w io.Writer, id inframill.InstanceID, c inframill.CommandExecution) error {
	v := clioutput.Details([]string{"API version", resultVersion}, []string{"Instance UUID", string(id)}, []string{"Command UUID", string(c.Request.ID)}, []string{"Command", c.Request.Command}, []string{"Status", string(c.Status)}, []string{"Created at", historyTime(&c.CreatedAt)}, []string{"Started at", historyTime(c.StartedAt)}, []string{"Ended at", historyTime(c.EndedAt)})
	if c.Error != nil {
		v.Rows = append(v.Rows, []string{"Error code", string(c.Error.Code)}, []string{"Error message", c.Error.Message}, []string{"Error path", c.Error.Path})
	}
	if c.Request.Args != nil {
		args, err := json.Marshal(c.Request.Args)
		if err != nil {
			return err
		}
		v.Sections = append(v.Sections, clioutput.Section{Name: "Args", JSON: args})
	}
	v.Sections = append(v.Sections, clioutput.Section{Name: "Result", JSON: c.Result})
	return v.Write(w)
}
