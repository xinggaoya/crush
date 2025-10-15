package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/xinggaoya/crush/internal/csync"
	"github.com/xinggaoya/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type DiagnosticsParams struct {
	FilePath string `json:"file_path"`
}

type diagnosticsTool struct {
	lspClients *csync.Map[string, *lsp.Client]
}

const DiagnosticsToolName = "diagnostics"

//go:embed diagnostics.md
var diagnosticsDescription []byte

func NewDiagnosticsTool(lspClients *csync.Map[string, *lsp.Client]) BaseTool {
	return &diagnosticsTool{
		lspClients,
	}
}

func (b *diagnosticsTool) Name() string {
	return DiagnosticsToolName
}

func (b *diagnosticsTool) Info() ToolInfo {
	return ToolInfo{
		Name:        DiagnosticsToolName,
		Description: string(diagnosticsDescription),
		Parameters: map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The path to the file to get diagnostics for (leave w empty for project diagnostics)",
			},
		},
		Required: []string{},
	}
}

func (b *diagnosticsTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params DiagnosticsParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %s", err)), nil
	}

	if b.lspClients.Len() == 0 {
		return NewTextErrorResponse("no LSP clients available"), nil
	}
	notifyLSPs(ctx, b.lspClients, params.FilePath)
	output := getDiagnostics(params.FilePath, b.lspClients)
	return NewTextResponse(output), nil
}

func notifyLSPs(ctx context.Context, lsps *csync.Map[string, *lsp.Client], filepath string) {
	if filepath == "" {
		return
	}
	for client := range lsps.Seq() {
		if !client.HandlesFile(filepath) {
			continue
		}
		_ = client.OpenFileOnDemand(ctx, filepath)
		_ = client.NotifyChange(ctx, filepath)
		client.WaitForDiagnostics(ctx, 5*time.Second)
	}
}

func getDiagnostics(filePath string, lsps *csync.Map[string, *lsp.Client]) string {
	fileDiagnostics := []string{}
	projectDiagnostics := []string{}

	for lspName, client := range lsps.Seq2() {
		for location, diags := range client.GetDiagnostics() {
			path, err := location.Path()
			if err != nil {
				slog.Error("Failed to convert diagnostic location URI to path", "uri", location, "error", err)
				continue
			}
			isCurrentFile := path == filePath
			for _, diag := range diags {
				formattedDiag := formatDiagnostic(path, diag, lspName)
				if isCurrentFile {
					fileDiagnostics = append(fileDiagnostics, formattedDiag)
				} else {
					projectDiagnostics = append(projectDiagnostics, formattedDiag)
				}
			}
		}
	}

	sortDiagnostics(fileDiagnostics)
	sortDiagnostics(projectDiagnostics)

	var output strings.Builder
	writeDiagnostics(&output, "file_diagnostics", fileDiagnostics)
	writeDiagnostics(&output, "project_diagnostics", projectDiagnostics)

	if len(fileDiagnostics) > 0 || len(projectDiagnostics) > 0 {
		fileErrors := countSeverity(fileDiagnostics, "Error")
		fileWarnings := countSeverity(fileDiagnostics, "Warn")
		projectErrors := countSeverity(projectDiagnostics, "Error")
		projectWarnings := countSeverity(projectDiagnostics, "Warn")
		output.WriteString("\n<diagnostic_summary>\n")
		fmt.Fprintf(&output, "Current file: %d errors, %d warnings\n", fileErrors, fileWarnings)
		fmt.Fprintf(&output, "Project: %d errors, %d warnings\n", projectErrors, projectWarnings)
		output.WriteString("</diagnostic_summary>\n")
	}

	out := output.String()
	slog.Info("Diagnostics", "output", fmt.Sprintf("%q", out))
	return out
}

func writeDiagnostics(output *strings.Builder, tag string, in []string) {
	if len(in) == 0 {
		return
	}
	output.WriteString("\n<" + tag + ">\n")
	if len(in) > 10 {
		output.WriteString(strings.Join(in[:10], "\n"))
		fmt.Fprintf(output, "\n... and %d more diagnostics", len(in)-10)
	} else {
		output.WriteString(strings.Join(in, "\n"))
	}
	output.WriteString("\n</" + tag + ">\n")
}

func sortDiagnostics(in []string) []string {
	sort.Slice(in, func(i, j int) bool {
		iIsError := strings.HasPrefix(in[i], "Error")
		jIsError := strings.HasPrefix(in[j], "Error")
		if iIsError != jIsError {
			return iIsError // Errors come first
		}
		return in[i] < in[j] // Then alphabetically
	})
	return in
}

func formatDiagnostic(pth string, diagnostic protocol.Diagnostic, source string) string {
	severity := "Info"
	switch diagnostic.Severity {
	case protocol.SeverityError:
		severity = "Error"
	case protocol.SeverityWarning:
		severity = "Warn"
	case protocol.SeverityHint:
		severity = "Hint"
	}

	location := fmt.Sprintf("%s:%d:%d", pth, diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1)

	sourceInfo := ""
	if diagnostic.Source != "" {
		sourceInfo = diagnostic.Source
	} else if source != "" {
		sourceInfo = source
	}

	codeInfo := ""
	if diagnostic.Code != nil {
		codeInfo = fmt.Sprintf("[%v]", diagnostic.Code)
	}

	tagsInfo := ""
	if len(diagnostic.Tags) > 0 {
		tags := []string{}
		for _, tag := range diagnostic.Tags {
			switch tag {
			case protocol.Unnecessary:
				tags = append(tags, "unnecessary")
			case protocol.Deprecated:
				tags = append(tags, "deprecated")
			}
		}
		if len(tags) > 0 {
			tagsInfo = fmt.Sprintf(" (%s)", strings.Join(tags, ", "))
		}
	}

	return fmt.Sprintf("%s: %s [%s]%s%s %s",
		severity,
		location,
		sourceInfo,
		codeInfo,
		tagsInfo,
		diagnostic.Message)
}

func countSeverity(diagnostics []string, severity string) int {
	count := 0
	for _, diag := range diagnostics {
		if strings.HasPrefix(diag, severity) {
			count++
		}
	}
	return count
}
