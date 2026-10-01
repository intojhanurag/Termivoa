package protocol

import (
	"strings"
	"testing"
)

func TestFilesMessagesKeepTheirBounds(t *testing.T) {
	size, text := int64(3), "hi\n"
	clientValid := map[MessageType]any{
		FilesList:   FilesListPayload{Path: ""},
		FilesRead:   FilesReadPayload{Path: "../not/refused/here"}, // the broker refuses it, keeping the stream
		FilesSearch: FilesSearchPayload{Query: strings.Repeat("é", MaxFileQueryRunes)},
	}
	for kind, payload := range clientValid {
		if _, err := DecodeClient(encodeTestMessage(t, kind, payload, true)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if _, err := DecodeClient(encodeTestMessage(t, kind, payload, false)); err == nil {
			t.Fatalf("%s without session identity was accepted", kind)
		}
	}
	clientInvalid := map[string]struct {
		kind    MessageType
		payload any
	}{
		"empty read":   {FilesRead, FilesReadPayload{Path: ""}},
		"long path":    {FilesList, FilesListPayload{Path: strings.Repeat("a", 4097)}},
		"NUL path":     {FilesList, FilesListPayload{Path: "a\x00"}},
		"empty query":  {FilesSearch, FilesSearchPayload{Query: ""}},
		"long query":   {FilesSearch, FilesSearchPayload{Query: strings.Repeat("a", MaxFileQueryRunes+1)}},
		"host message": {FilesListed, FilesListedPayload{State: FilesListStateReady, Entries: []FileEntry{}}},
	}
	for name, test := range clientInvalid {
		if _, err := DecodeClient(encodeTestMessage(t, test.kind, test.payload, true)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}

	root := "C:/Users/me/app"
	hostValid := map[MessageType]any{
		FilesListed: FilesListedPayload{State: FilesListStateReady, Root: &root, Path: "src", Truncated: true, Entries: []FileEntry{
			{Name: "lib", Kind: Dir}, {Name: "main.go", Kind: File, Size: &size}, {Name: "out", Kind: Link}, {Name: "fifo", Kind: FileKindOther},
		}},
		FilesContent: FilesContentPayload{State: FilesReadStateReady, Path: "src/main.go", Size: &size, Text: &text},
		FilesFound:   FilesFoundPayload{State: FilesSearchStateReady, Query: "main", Paths: []string{"src/main.go"}},
	}
	for kind, payload := range hostValid {
		if _, err := DecodeHost(encodeTestMessage(t, kind, payload, true)); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	big, nul := strings.Repeat("a", MaxFileTextBytes+1), "a\x00"
	hostInvalid := map[string]struct {
		kind    MessageType
		payload any
	}{
		"nil entries":         {FilesListed, FilesListedPayload{State: FilesListStateReady}},
		"entries not ready":   {FilesListed, FilesListedPayload{State: NotDir, Entries: []FileEntry{{Name: "a", Kind: Dir}}}},
		"dir with size":       {FilesListed, FilesListedPayload{State: FilesListStateReady, Entries: []FileEntry{{Name: "a", Kind: Dir, Size: &size}}}},
		"file without size":   {FilesListed, FilesListedPayload{State: FilesListStateReady, Entries: []FileEntry{{Name: "a", Kind: File}}}},
		"slash name":          {FilesListed, FilesListedPayload{State: FilesListStateReady, Entries: []FileEntry{{Name: "a/b", Kind: Dir}}}},
		"too many entries":    {FilesListed, FilesListedPayload{State: FilesListStateReady, Entries: make([]FileEntry, MaxFileEntries+1)}},
		"text when binary":    {FilesContent, FilesContentPayload{State: FilesReadStateBinary, Path: "a", Text: &text}},
		"ready without text":  {FilesContent, FilesContentPayload{State: FilesReadStateReady, Path: "a"}},
		"text over the limit": {FilesContent, FilesContentPayload{State: FilesReadStateReady, Path: "a", Text: &big}},
		"NUL in text":         {FilesContent, FilesContentPayload{State: FilesReadStateReady, Path: "a", Text: &nul}},
		"paths unavailable":   {FilesFound, FilesFoundPayload{State: FilesSearchStateUnavailable, Query: "a", Paths: []string{"a"}}},
		"too many paths":      {FilesFound, FilesFoundPayload{State: FilesSearchStateReady, Query: "a", Paths: make([]string, MaxFoundPaths+1)}},
		"client message":      {FilesSearch, FilesSearchPayload{Query: "a"}},
	}
	for name, test := range hostInvalid {
		if _, err := DecodeHost(encodeTestMessage(t, test.kind, test.payload, true)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
