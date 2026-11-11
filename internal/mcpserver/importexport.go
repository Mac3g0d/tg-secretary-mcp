package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mac3g0d/tg-secretary-mcp/internal/tgexport"
)

// _maxInlineExport caps an export passed in the tool call itself, which has
// to fit in memory whole. Bigger exports go through the import directory.
const _maxInlineExport = 8 << 20

// MaxRequestBodyBytes is the HTTP request size the MCP endpoint must accept
// for an inline export of the maximum size to get through. The margin covers
// JSON escaping, which can triple the size of non-ASCII text.
const MaxRequestBodyBytes = 4 * _maxInlineExport

type importInput struct {
	Content string `json:"content,omitempty" jsonschema:"the export itself: the full text of result.json, up to 8 MB; use this when you have the file and the secretary runs elsewhere"`
	File    string `json:"file,omitempty" jsonschema:"alternative to content for large exports: name of a file already placed in the import directory on the secretary's machine, e.g. result.json or ivan/result.json"`
}

type skippedChat struct {
	Chat string `json:"chat"`
	Type string `json:"type" jsonschema:"kind of chat as named in the export, e.g. private_supergroup"`
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
	Chats     []importedChat `json:"chats,omitempty" jsonschema:"result per imported personal chat"`
	Skipped   []skippedChat  `json:"skipped_chats,omitempty" jsonschema:"chats left out because only personal chats are imported"`
}

func (t *tools) addImportChatExport(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "import_chat_export",
		Description: "Load the history of a personal (one-to-one) chat from a Telegram " +
			"Desktop JSON export (result.json, media not needed) into the archive. Pass " +
			"the text of the file as content, or, for exports over 8 MB, the name of a " +
			"file placed in the import directory on the secretary's machine. Call " +
			"without arguments to see that directory and the files in it. Groups, " +
			"channels and bot chats are not imported. Importing the same export again " +
			"adds nothing, and imported history never shows up in get_new_messages.",
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
	name := strings.TrimSpace(in.File)

	if in.Content != "" {
		if name != "" {
			return nil, out, errors.New("pass either content or file, not both")
		}
		if len(in.Content) > _maxInlineExport {
			return nil, out, fmt.Errorf(
				"content is %d bytes, the limit is %d: put the file into %s and pass its name as file",
				len(in.Content), _maxInlineExport, out.Directory)
		}
		err := t.importFrom(ctx, strings.NewReader(in.Content), &out)
		return nil, out, err
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

	if err := t.importFrom(ctx, f, &out); err != nil {
		return nil, out, fmt.Errorf("%q: %w", name, err)
	}
	return nil, out, nil
}

// importFrom imports the export read from r and records the outcome in out.
// Chats stored before a failure are reported too.
func (t *tools) importFrom(ctx context.Context, r io.Reader, out *importOutput) error {
	res, err := tgexport.Import(ctx, t.store, r)
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
	for _, c := range res.Unsupported {
		out.Skipped = append(out.Skipped, skippedChat{Chat: c.Title, Type: c.Type})
	}
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	return nil
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
