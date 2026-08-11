package claudecode

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// Tailer reads only the bytes appended to each transcript file since the
// last call for that path, so a poll cycle never re-processes old lines.
type Tailer struct {
	offsets map[string]int64
}

// NewTailer returns a Tailer with no prior read history.
func NewTailer() *Tailer {
	return &Tailer{offsets: make(map[string]int64)}
}

// readNewLinesFrom returns the complete lines from reader, updating the
// stored offset for path. It properly distinguishes io.EOF (expected end)
// from real I/O errors (which are propagated).
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
				// Real error, not EOF — persist offset for lines we did read
				// before propagating the error
				t.offsets[path] = consumed
				return lines, err
			}
			break // EOF — normal end of file, might have incomplete line
		}
	}
	t.offsets[path] = consumed
	return lines, nil
}

// ReadNewLines returns the complete lines appended to path since the
// last call for that path. An incomplete trailing line (the file is
// still being written) is left unconsumed for the next call.
func (t *Tailer) ReadNewLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	offset := t.offsets[path]
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	return t.readNewLinesFrom(path, f, offset)
}
