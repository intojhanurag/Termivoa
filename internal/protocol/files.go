package protocol

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Files view bounds (files.list, files.read, files.search and replies).
const (
	MaxFileEntries     = 1000
	MaxFoundPaths      = 200
	MaxFileTextBytes   = 256 << 10
	MaxFileQueryRunes  = 200
	maxFileNameRunes   = 255
	maxFilePathRunes   = maxChangePathRunes
	maxFileFolderRunes = maxChangePathRunes
)

// validFilePath accepts 0..4096 characters of UTF-8 without NUL. Absolute
// paths and .. segments pass here and are refused by the broker with an
// invalid_request error, so a bad path does not end the stream.
func validFilePath(path string, empty bool) bool {
	return (empty || path != "") && utf8.ValidString(path) &&
		utf8.RuneCountInString(path) <= maxFilePathRunes && strings.IndexByte(path, 0) < 0
}

// ValidFileQuery accepts 1..200 characters of UTF-8 without NUL.
func ValidFileQuery(query string) bool {
	return query != "" && utf8.ValidString(query) && utf8.RuneCountInString(query) <= MaxFileQueryRunes &&
		strings.IndexByte(query, 0) < 0
}

func validateFilesList(payload FilesListPayload) error {
	if !validFilePath(payload.Path, true) {
		return errors.New("files path is outside bounds")
	}
	return nil
}

func validateFilesRead(payload FilesReadPayload) error {
	if !validFilePath(payload.Path, false) {
		return errors.New("files path is outside bounds")
	}
	return nil
}

func validateFilesSearch(payload FilesSearchPayload) error {
	if !ValidFileQuery(payload.Query) {
		return errors.New("files query is outside bounds")
	}
	return nil
}

func validateFilesRoot(root *string) error {
	if root != nil && (*root == "" || !utf8.ValidString(*root) || utf8.RuneCountInString(*root) > maxFileFolderRunes) {
		return errors.New("files root is outside bounds")
	}
	return nil
}

func validateFilesListed(payload FilesListedPayload) error {
	switch payload.State {
	case FilesListStateReady, FilesListStateNotFound, NotDir, FilesListStateUnavailable:
	default:
		return errors.New("invalid files list state")
	}
	if !validFilePath(payload.Path, true) {
		return errors.New("files path is outside bounds")
	}
	if err := validateFilesRoot(payload.Root); err != nil {
		return err
	}
	if payload.Entries == nil || len(payload.Entries) > MaxFileEntries ||
		(payload.State != FilesListStateReady && (len(payload.Entries) > 0 || payload.Truncated)) {
		return errors.New("file entries are outside bounds")
	}
	for _, entry := range payload.Entries {
		name := entry.Name
		if name == "" || name == "." || name == ".." || !utf8.ValidString(name) ||
			utf8.RuneCountInString(name) > maxFileNameRunes || strings.ContainsAny(name, "/\x00") {
			return errors.New("file name is outside bounds")
		}
		switch entry.Kind {
		case File:
			if entry.Size == nil || *entry.Size < 0 {
				return errors.New("a file entry needs a size")
			}
		case Dir, Link, FileKindOther:
			if entry.Size != nil {
				return errors.New("only a file entry has a size")
			}
		default:
			return errors.New("invalid file kind")
		}
	}
	return nil
}

func validateFilesContent(payload FilesContentPayload) error {
	switch payload.State {
	case FilesReadStateReady, FilesReadStateBinary, TooLarge, FilesReadStateNotFound, NotFile, FilesReadStateUnavailable:
	default:
		return errors.New("invalid files read state")
	}
	if !validFilePath(payload.Path, false) {
		return errors.New("files path is outside bounds")
	}
	if payload.Size != nil && *payload.Size < 0 {
		return errors.New("file size must not be negative")
	}
	ready := payload.State == FilesReadStateReady
	if (payload.Text != nil) != ready || (payload.Truncated && !ready) {
		return errors.New("only ready carries text")
	}
	if text := valueOrEmpty(payload.Text); len(text) > MaxFileTextBytes || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return errors.New("file text is outside bounds")
	}
	return nil
}

func validateFilesFound(payload FilesFoundPayload) error {
	if payload.State != FilesSearchStateReady && payload.State != FilesSearchStateUnavailable {
		return errors.New("invalid files search state")
	}
	if !ValidFileQuery(payload.Query) {
		return errors.New("files query is outside bounds")
	}
	if payload.Paths == nil || len(payload.Paths) > MaxFoundPaths ||
		(payload.State != FilesSearchStateReady && (len(payload.Paths) > 0 || payload.Truncated)) {
		return errors.New("found paths are outside bounds")
	}
	for _, path := range payload.Paths {
		if !validFilePath(path, false) {
			return errors.New("found path is outside bounds")
		}
	}
	return nil
}
