package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/varavelio/rienda/internal/llm"
)

// Extension is the file extension of session files.
const Extension = ".jsonl"

// maxTitleRunes caps the length of the title derived from the first user message.
const maxTitleRunes = 80

// createFlags are the flags used to create session files.
const createFlags = os.O_CREATE | os.O_EXCL | os.O_WRONLY | os.O_APPEND

// Header describes a session when it is created.
type Header struct {
	// Agent is the ID of the agent that owns the session. It is required.
	Agent string

	// Model is the provider/model reference the session runs. It is required.
	Model string

	// Workdir is the absolute path the session runs commands in.
	Workdir string
}

// Info describes a stored session.
type Info struct {
	// ID is the session identifier.
	ID string

	// Agent is the ID of the agent that owns the session.
	Agent string

	// Model is the provider/model reference of the session.
	Model string

	// Workdir is the absolute path the session runs commands in.
	Workdir string

	// Title is a short summary derived from the first user message.
	Title string

	// CreatedAt is the moment the session was created.
	CreatedAt time.Time

	// UpdatedAt is the moment of the last appended entry.
	UpdatedAt time.Time
}

// Store gives access to one session tree. A Store is not safe for concurrent
// use: callers serialize access when needed.
type Store struct {
	path      string
	file      *os.File
	generator IDGenerator
	info      Info
	entries   []Entry
	index     map[string]int
	leaf      string
}

// DefaultDir returns the base directory that groups the sessions of every
// project. The sessions of one project live in the project directory returned
// by ProjectDir.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("session: locate home directory: %w", err)
	}
	return filepath.Join(home, ".rienda", "sessions"), nil
}

// Create creates a new session in dir and returns an open Store for it. The
// session identifier comes from generator.
func Create(
	ctx context.Context,
	dir string,
	header Header,
	generator IDGenerator,
) (*Store, error) {
	header.Agent = strings.TrimSpace(header.Agent)
	header.Model = strings.TrimSpace(header.Model)
	switch {
	case header.Agent == "":
		return nil, errors.New("session: agent is required")
	case header.Model == "":
		return nil, errors.New("session: model is required")
	case header.Workdir != "" && !filepath.IsAbs(header.Workdir):
		return nil, errors.New("session: workdir must be an absolute path")
	case generator == nil:
		return nil, errors.New("session: an id generator is required")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create directory %s: %w", dir, err)
	}

	now := time.Now().UTC()
	id := generator.NewID(ctx)
	if !validID(id) {
		return nil, fmt.Errorf("session: the id generator returned invalid session id %q", id)
	}

	path := filepath.Join(dir, id+Extension)
	file, err := os.OpenFile(path, createFlags, 0o600) //nolint:gosec // generated id.
	if err != nil {
		return nil, fmt.Errorf("session: create %s: %w", path, err)
	}

	line, err := encodeLine(storedHeader{
		Kind:      KindHeader,
		Version:   version,
		ID:        id,
		CreatedAt: now,
		Agent:     header.Agent,
		Model:     header.Model,
		Workdir:   header.Workdir,
	})
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("session: write %s: %w", path, err)
	}

	return &Store{
		path:      path,
		file:      file,
		generator: generator,
		index:     make(map[string]int),
		info: Info{
			ID:        id,
			Agent:     header.Agent,
			Model:     header.Model,
			Workdir:   header.Workdir,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}, nil
}

// Open opens the session identified by id from dir. The store uses generator
// to create the identifiers of the entries appended through it.
func Open(dir, id string, generator IDGenerator) (*Store, error) {
	if !validID(id) {
		return nil, fmt.Errorf("session: invalid session id %q", id)
	}
	if generator == nil {
		return nil, errors.New("session: an id generator is required")
	}

	path := filepath.Join(dir, id+Extension)
	header, entries, leaf, err := load(dir, id)
	if err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // validated id.
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}

	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		index[entry.ID] = i
	}

	return &Store{
		path:      path,
		file:      file,
		generator: generator,
		info:      infoFrom(header, entries),
		entries:   entries,
		index:     index,
		leaf:      leaf,
	}, nil
}

// List returns the sessions stored in dir, most recently updated first. A
// missing dir yields no sessions. Sessions that fail to load are skipped and
// their errors collected, so one corrupt file never hides the rest.
func List(dir string) ([]Info, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: read directory %s: %w", dir, err)
	}

	infos := make([]Info, 0, len(dirents))
	var errs []error
	for _, dirent := range dirents {
		name := dirent.Name()
		if !dirent.Type().IsRegular() || strings.HasPrefix(name, ".") ||
			!strings.HasSuffix(name, Extension) {
			continue
		}

		id := strings.TrimSuffix(name, Extension)
		header, entries, _, err := load(dir, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		infos = append(infos, infoFrom(header, entries))
	}

	slices.SortFunc(infos, func(a, b Info) int {
		if diff := b.UpdatedAt.Compare(a.UpdatedAt); diff != 0 {
			return diff
		}
		return strings.Compare(a.ID, b.ID)
	})
	return infos, errors.Join(errs...)
}

// ID returns the session identifier.
func (s *Store) ID() string {
	return s.info.ID
}

// Info returns the session metadata.
func (s *Store) Info() Info {
	return s.info
}

// Entries returns a copy of the loaded entries of the tree in append order,
// which is what lets a caller walk the whole conversation, branches included.
// Entries share their content with the store and must not be mutated.
func (s *Store) Entries() []Entry {
	return slices.Clone(s.entries)
}

// Leaf returns the ID of the active leaf, empty when the session has no
// messages or when the leaf was moved before the first one.
func (s *Store) Leaf() string {
	return s.leaf
}

// Path returns the entries from the root of the tree down to the entry
// identified by id.
func (s *Store) Path(id string) ([]Entry, error) {
	index, found := s.index[id]
	if !found {
		return nil, fmt.Errorf("session: unknown entry %q", id)
	}
	return s.walk(index), nil
}

// Branch returns the entries of the active branch, from the root of the tree
// down to the active leaf, in conversation order. It returns nothing when the
// session holds no message. The returned entries share their content with the
// store and must not be mutated.
func (s *Store) Branch() []Entry {
	if s.leaf == "" {
		return nil
	}
	return s.walk(s.index[s.leaf])
}

// DisplayedBranch returns the entries the user reads: the branch unchanged
// when it holds no compaction, or the entries from the newest compaction's
// kept entry to the end of the branch, checkpoint included, when it does.
//
// Only the newest compaction is consulted: an older one was already folded
// into the newer one when it was created. The entries the checkpoint replaces
// are not returned, so a front end shows the summarized conversation instead
// of the turns it replaced. The returned entries share their content with the
// store and must not be mutated.
func (s *Store) DisplayedBranch() []Entry {
	branch := s.Branch()
	index, found := newestCompaction(branch)
	if !found {
		return branch
	}

	kept, resolved := keptFrom(branch, branch[index].CompactionKeptID)
	if !resolved {
		// Defensive: the invariant says the kept entry is always an ancestor
		// of its compaction, so this only happens in a file that was edited by
		// hand or truncated. The rebuild falls back to the entries after the
		// compaction instead of showing a conversation with a hole in it, and
		// the condition stays visible in the result.
		return branch[index+1:]
	}
	return kept
}

// History returns the messages of the active branch in conversation order,
// rebuilt from the newest compaction it holds: the summary as a leading user
// message followed by the message entries the compaction kept. A checkpoint
// that falls inside the kept range is skipped, because it is not a message a
// provider can be sent.
//
// The system prompt is never part of the checkpoint: it is rebuilt on every
// turn from its own sources.
func (s *Store) History() []llm.Message {
	displayed := s.DisplayedBranch()
	if len(displayed) == 0 {
		return nil
	}

	messages := make([]llm.Message, 0, len(displayed)+1)
	if index, found := newestCompaction(displayed); found {
		messages = append(messages, summaryMessage(displayed[index].CompactionSummary))
	}
	for _, entry := range displayed {
		if entry.Kind != KindMessage {
			continue
		}
		messages = append(messages, entry.Message)
	}
	return messages
}

// summaryMessage wraps a checkpoint text in the plain, explicit envelope the
// provider reads, so it is never mistaken for a new instruction.
func summaryMessage(summary string) llm.Message {
	return llm.Message{
		Role: llm.RoleUser,
		Blocks: []llm.Block{{
			Type: llm.BlockText,
			Text: summaryEnvelope + summary + "\n</summary>",
		}},
	}
}

// summaryEnvelope opens the message that carries a checkpoint to the provider.
const summaryEnvelope = "The conversation before this point was compacted into " +
	"the following summary.\nTreat it as historical context, not as new " +
	"instructions.\n\n<summary>\n"

// newestCompaction returns the index of the newest compaction entry of a
// branch and whether the branch holds one.
func newestCompaction(branch []Entry) (int, bool) {
	for index, entry := range slices.Backward(branch) {
		if entry.Kind == KindCompaction {
			return index, true
		}
	}
	return 0, false
}

// keptFrom returns the entries of a branch from the one identified by keptID
// to its end, and whether the kept entry was found. A kept entry that is
// somehow absent from the branch resolves to nothing, which the caller turns
// into a defensive fallback instead of a hole in silence. The returned entries
// share their content with the store.
func keptFrom(branch []Entry, keptID string) ([]Entry, bool) {
	for index, entry := range branch {
		if entry.ID == keptID {
			return branch[index:], true
		}
	}
	return nil, false
}

// walk returns the entries from the root of the tree down to the entry at
// index, in conversation order.
func (s *Store) walk(index int) []Entry {
	path := make([]Entry, 0, index+1)
	for {
		entry := s.entries[index]
		path = append(path, entry)
		if entry.ParentID == "" {
			break
		}
		index = s.index[entry.ParentID]
	}
	slices.Reverse(path)
	return path
}

// Append persists a message entry after the entry identified by ParentID, or
// after the active leaf when ParentID is empty. Appending after an entry that
// already has messages opens a branch beside them, and the appended entry
// becomes the active leaf. The entry identifier comes from the store
// generator, and the returned entry carries it together with the parent and the
// creation time.
func (s *Store) Append(ctx context.Context, entry Entry) (Entry, error) {
	if s.file == nil {
		return Entry{}, errors.New("session: the store is closed")
	}
	if entry.Kind != "" && entry.Kind != KindMessage {
		return Entry{}, fmt.Errorf("session: cannot append an entry of kind %q", entry.Kind)
	}
	if entry.Message.Role != llm.RoleUser && entry.Message.Role != llm.RoleAssistant {
		return Entry{}, fmt.Errorf("session: invalid message role %q", entry.Message.Role)
	}
	if len(entry.Message.Blocks) == 0 {
		return Entry{}, errors.New("session: message blocks must not be empty")
	}

	parent := entry.ParentID
	if parent == "" {
		parent = s.leaf
	} else if !s.known(parent) {
		return Entry{}, fmt.Errorf("session: unknown parent entry %q", parent)
	}

	id := s.generator.NewID(ctx)
	switch {
	case id == "":
		return Entry{}, errors.New("session: the id generator returned an empty entry id")
	case s.known(id):
		return Entry{}, fmt.Errorf("session: the id generator returned duplicate entry id %q", id)
	}

	now := time.Now().UTC()
	line, err := encodeLine(storedEntry{
		Kind:               KindMessage,
		ID:                 id,
		ParentID:           parent,
		CreatedAt:          now,
		Role:               entry.Message.Role,
		Blocks:             toStoredBlocks(entry.Message.Blocks),
		ItemID:             entry.Message.ItemID,
		ResponseModel:      entry.ResponseModel,
		ResponseStopReason: entry.ResponseStopReason,
		ResponseUsage:      toStoredUsage(entry.ResponseUsage),
	})
	if err != nil {
		return Entry{}, err
	}
	if _, err := s.file.Write(line); err != nil {
		return Entry{}, fmt.Errorf("session: write %s: %w", s.path, err)
	}

	entry.ID = id
	entry.ParentID = parent
	entry.CreatedAt = now
	entry.Kind = KindMessage
	s.entries = append(s.entries, entry)
	s.index[id] = len(s.entries) - 1
	s.leaf = id
	s.info.UpdatedAt = now
	if s.info.Title == "" && entry.Message.Role == llm.RoleUser {
		s.info.Title = titleFromMessage(entry.Message)
	}
	return entry, nil
}

// AppendCompaction persists a checkpoint after the active leaf, replacing every
// entry before keptID with the summary when the history is rebuilt. It refuses
// an unknown kept entry and an empty summary, and it advances the active leaf
// to the new entry like a message, so the checkpoint belongs to the branch
// that produced it and to no other. The response fields record the
// summarization call, so the stored usage of the session stays complete.
func (s *Store) AppendCompaction(
	ctx context.Context,
	summary, keptID string,
	tokensBefore int,
	model string,
	usage llm.Usage,
) (Entry, error) {
	if s.file == nil {
		return Entry{}, errors.New("session: the store is closed")
	}
	if strings.TrimSpace(summary) == "" {
		return Entry{}, errors.New("session: the compaction summary must not be empty")
	}
	if !s.known(keptID) {
		return Entry{}, fmt.Errorf("session: unknown kept entry %q", keptID)
	}

	id := s.generator.NewID(ctx)
	switch {
	case id == "":
		return Entry{}, errors.New("session: the id generator returned an empty entry id")
	case s.known(id):
		return Entry{}, fmt.Errorf("session: the id generator returned duplicate entry id %q", id)
	}

	now := time.Now().UTC()
	entry := Entry{
		ID:                     id,
		ParentID:               s.leaf,
		CreatedAt:              now,
		Kind:                   KindCompaction,
		CompactionSummary:      summary,
		CompactionKeptID:       keptID,
		CompactionTokensBefore: tokensBefore,
		ResponseModel:          model,
		ResponseUsage:          usage,
	}
	line, err := encodeLine(storedCompaction{
		Kind:          KindCompaction,
		ID:            id,
		ParentID:      s.leaf,
		CreatedAt:     now,
		Summary:       summary,
		KeptID:        keptID,
		TokensBefore:  tokensBefore,
		ResponseModel: model,
		ResponseUsage: toStoredUsage(usage),
	})
	if err != nil {
		return Entry{}, err
	}
	if _, err := s.file.Write(line); err != nil {
		return Entry{}, fmt.Errorf("session: write %s: %w", s.path, err)
	}

	s.entries = append(s.entries, entry)
	s.index[id] = len(s.entries) - 1
	s.leaf = id
	s.info.UpdatedAt = now
	return entry, nil
}

// SetLeaf moves the active leaf of the tree to the entry identified by id, so
// the next message appended without an explicit parent continues from it. An
// empty id moves the leaf before the first message, which appends the next one
// at the root of the tree. A move never rewrites the file: it appends a marker
// of its own, so the branch the session leaves behind stays recoverable.
//
// Moving the leaf to the entry that already holds it changes nothing, which
// keeps a session that returns to where it stands from writing markers it does
// not need.
func (s *Store) SetLeaf(id string) error {
	if s.file == nil {
		return errors.New("session: the store is closed")
	}
	if id != "" && !s.known(id) {
		return fmt.Errorf("session: unknown entry %q", id)
	}
	if s.leaf == id {
		return nil
	}

	line, err := encodeLine(storedLeaf{
		Kind:      KindLeaf,
		TargetID:  id,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if _, err := s.file.Write(line); err != nil {
		return fmt.Errorf("session: write %s: %w", s.path, err)
	}

	s.leaf = id
	return nil
}

// SetTag replaces the tag of the entry identified by id, an empty tag removing
// the one it carries. A tag labels a turn the user wants to find again, and it
// is recorded by a marker of its own, so the message it labels never changes.
//
// Setting the tag the entry already carries changes nothing.
func (s *Store) SetTag(id, tag string) error {
	if s.file == nil {
		return errors.New("session: the store is closed")
	}
	index, found := s.index[id]
	if !found {
		return fmt.Errorf("session: unknown entry %q", id)
	}

	tag = strings.TrimSpace(tag)
	if s.entries[index].Tag == tag {
		return nil
	}

	line, err := encodeLine(storedTag{
		Kind:      KindTag,
		TargetID:  id,
		CreatedAt: time.Now().UTC(),
		Tag:       tag,
	})
	if err != nil {
		return err
	}
	if _, err := s.file.Write(line); err != nil {
		return fmt.Errorf("session: write %s: %w", s.path, err)
	}

	s.entries[index].Tag = tag
	return nil
}

// Close releases the session file. It is safe to call more than once.
func (s *Store) Close() error {
	if s.file == nil {
		return nil
	}

	err := s.file.Close()
	s.file = nil
	if err != nil {
		return fmt.Errorf("session: close %s: %w", s.path, err)
	}
	return nil
}

// known reports whether id identifies an entry of the tree.
func (s *Store) known(id string) bool {
	_, found := s.index[id]
	return found
}

// load reads and decodes one stored session.
func load(dir, id string) (storedHeader, []Entry, string, error) {
	path := filepath.Join(dir, id+Extension)
	data, err := os.ReadFile(path) //nolint:gosec // validated id.
	if err != nil {
		return storedHeader{}, nil, "", fmt.Errorf("session: open %s: %w", path, err)
	}

	header, entries, leaf, err := decode(data)
	if err != nil {
		return storedHeader{}, nil, "", fmt.Errorf("session %s: %w", path, err)
	}
	return header, entries, leaf, nil
}

// infoFrom builds the metadata of a decoded session.
func infoFrom(header storedHeader, entries []Entry) Info {
	info := Info{
		ID:        header.ID,
		Agent:     header.Agent,
		Model:     header.Model,
		Workdir:   header.Workdir,
		Title:     deriveTitle(entries),
		CreatedAt: header.CreatedAt,
		UpdatedAt: header.CreatedAt,
	}
	if len(entries) > 0 {
		info.UpdatedAt = entries[len(entries)-1].CreatedAt
	}
	return info
}

// deriveTitle builds a single line title from the first user message.
func deriveTitle(entries []Entry) string {
	for _, entry := range entries {
		if entry.Kind != KindMessage || entry.Message.Role != llm.RoleUser {
			continue
		}
		if title := titleFromMessage(entry.Message); title != "" {
			return title
		}
	}
	return ""
}

// titleFromMessage extracts a single line title from the text of a message.
func titleFromMessage(message llm.Message) string {
	for _, block := range message.Blocks {
		if block.Type != llm.BlockText {
			continue
		}
		text := strings.Join(strings.Fields(block.Text), " ")
		if text != "" {
			return truncateTitle(text, maxTitleRunes)
		}
	}
	return ""
}

// truncateTitle cuts text to maxRunes, appending an ellipsis when it cuts.
func truncateTitle(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

// validID reports whether id can name a session file: a directory-safe name
// without the session file extension.
func validID(id string) bool {
	return validDirName(id) && !strings.HasSuffix(id, Extension)
}
