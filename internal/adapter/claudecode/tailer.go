package claudecode

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// DiscoverTranscriptFiles returns every Claude Code transcript file
// (*.jsonl) found under root, typically $HOME/.claude/projects. A
// missing root is not an error -- it simply means no transcripts exist
// yet.
func DiscoverTranscriptFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting the whole walk
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

type fileMeta struct {
	modTime time.Time
	size    int64
	offset  int64
}

// Tailer reads only the bytes appended to each transcript file since the
// last call for that path, caching file metadata to avoid reading unchanged files.
// Memory footprint: Each fileMeta entry is ~40 bytes, so caching 10,000 historical
// transcript files uses <500KB of RAM, remaining entirely negligible.
type Tailer struct {
	files map[string]*fileMeta
}

// NewTailer returns a Tailer with no prior read history.
func NewTailer() *Tailer {
	return &Tailer{files: make(map[string]*fileMeta)}
}

func (t *Tailer) readNewLinesFrom(path string, reader io.Reader, offset int64) ([][]byte, error) {
	var lines [][]byte
	bufReader := bufio.NewReader(reader)
	consumed := offset
	for {
		line, err := bufReader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			lines = append(lines, line[:len(line)-1])
			consumed += int64(len(line))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.getOrCreateMeta(path).offset = consumed
				return lines, err
			}
			break
		}
	}
	t.getOrCreateMeta(path).offset = consumed
	return lines, nil
}

func (t *Tailer) getOrCreateMeta(path string) *fileMeta {
	meta, ok := t.files[path]
	if !ok {
		meta = &fileMeta{}
		t.files[path] = meta
	}
	return meta
}

// ReadNewLines returns complete lines appended to path, whether the file changed, and any error.
// An incomplete trailing line (the file is still being written) is left unconsumed for the next call.
func (t *Tailer) ReadNewLines(path string) ([][]byte, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}

	meta := t.getOrCreateMeta(path)

	// Dirty-check: if modTime and size match our cached state, skip file completely
	if !meta.modTime.IsZero() && info.ModTime().Equal(meta.modTime) && info.Size() == meta.size {
		return nil, false, nil
	}

	// Truncation check: if file shrunk below our offset, reset to 0
	if info.Size() < meta.offset {
		meta.offset = 0
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	if _, err := f.Seek(meta.offset, io.SeekStart); err != nil {
		return nil, false, err
	}

	oldOffset := meta.offset
	lines, err := t.readNewLinesFrom(path, f, meta.offset)
	if err != nil {
		return lines, meta.offset != oldOffset, err
	}

	meta.modTime = info.ModTime()
	meta.size = info.Size()

	return lines, meta.offset != oldOffset, nil
}

// SeedOffset sets the starting byte offset for path. Only meaningful
// before the first ReadNewLines call for that path -- used to restore a
// persisted watermark at daemon startup.
func (t *Tailer) SeedOffset(path string, offset int64) {
	t.getOrCreateMeta(path).offset = offset
}

// Offset returns the current byte offset stored for path (0 if
// ReadNewLines has never been called for it).
func (t *Tailer) Offset(path string) int64 {
	return t.getOrCreateMeta(path).offset
}
