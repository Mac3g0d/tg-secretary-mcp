package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/tgexport"
)

type importInput struct {
	File string `json:"file,omitempty" jsonschema:"name of the export file inside the import directory, e.g. result.json or ivan/result.json; omit to list the files available for import"`
}

type importedChat struct {
	ChatID        int64  `json:"chat_id"`
	Chat          string `json:"chat"`
	Type          string `json:"type"`
	Imported      int64  `json:"imported" jsonschema:"messages added to the archive"`
	AlreadyStored int64  `json:"already_stored" jsonschema:"messages skipped because the archive already had them"`
	Skipped       int64  `json:"skipped" jsonschema:"entries without text or attachment, such as service messages"`
}

type importOutput struct {
	Directory string         `json:"directory" jsonschema:"the import directory on the machine running the secretary"`
	Files     []string       `json:"available_files,omitempty" jsonschema:"JSON files found in the import directory; returned when no file was given"`
	Chats     []importedChat `json:"chats,omitempty" jsonschema:"result per imported chat"`
}

func (t *tools) addImportChatExport(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "import_chat_export",
		Description: "Load chat history from a Telegram Desktop JSON export (result.json, " +
			"media not needed) into the archive. The file must first be placed in the " +
			"import directory on the machine running the secretary; call without " +
			"arguments to see that directory and the files in it. Importing the same " +
			"file again adds nothing, and imported history never shows up in " +
			"get_new_messages.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, t.importChatExport)
}

func (t *tools) importChatExport(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in importInput,
) (*mcp.CallToolResult, importOutput, error) {
	out := importOutput{Directory: t.importDir}
	if abs, err := filepath.Abs(t.importDir); err == nil {
		out.Directory = abs
	}

	// os.Root confines every open to the directory, symlinks included, so a
	// crafted file name cannot make the tool read anything else on the host.
	root, err := os.OpenRoot(t.importDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, out, fmt.Errorf(
			"import directory %s does not exist: create it and put the export there",
			out.Directory)
	}
	if err != nil {
		return nil, out, fmt.Errorf("open import directory: %w", err)
	}
	defer root.Close()

	name := strings.TrimSpace(in.File)
	if name == "" {
		out.Files, err = exportFiles(root)
		if err != nil {
			return nil, out, fmt.Errorf("list import directory: %w", err)
		}
		return nil, out, nil
	}

	f, err := root.Open(name)
	if err != nil {
		return nil, out, fmt.Errorf(
			"open %q in the import directory %s: %w", name, out.Directory, err)
	}
	defer f.Close()

	res, err := tgexport.Import(ctx, t.store, f)
	out.Chats = make([]importedChat, 0, len(res.Chats))
	for _, c := range res.Chats {
		out.Chats = append(out.Chats, importedChat{
			ChatID:        c.ChatID,
			Chat:          c.Title,
			Type:          c.Type,
			Imported:      c.Imported,
			AlreadyStored: c.AlreadyStored,
			Skipped:       c.Skipped,
		})
	}
	if err != nil {
		return nil, out, fmt.Errorf("import %q: %w", name, err)
	}
	return nil, out, nil
}

// exportFiles lists the JSON files under root, capped to keep the reply small.
func exportFiles(root *os.Root) ([]string, error) {
	const maxListedFiles = 200

	var files []string
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
			files = append(files, path)
		}
		if len(files) >= maxListedFiles {
			return fs.SkipAll
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}
