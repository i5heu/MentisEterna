package searchindex

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
)

const (
	ChunkTableName = "note_search_chunks"
	VecTableName   = "vss_note_search"
	VecColumnName  = "embedding"

	ChunkFieldTitle = "title"
	ChunkFieldPath  = "path"
	ChunkFieldTags  = "tags"
	ChunkFieldBody  = "body"
)

type Document struct {
	NoteID       int64
	Title        string
	Body         string
	PathSegments []string
	Tags         []string
}

type Chunk struct {
	Field   string
	Ordinal int
	Text    string
}

func (d Document) DisplayPath() string {
	return DisplayPath(d.PathSegments)
}

func (d Document) SearchPathText() string {
	return SearchPathText(d.PathSegments)
}

func DisplayPath(segments []string) string {
	return strings.Join(segments, ":")
}

func SearchPathText(segments []string) string {
	return strings.Join(segments, " / ")
}

func LoadDocument(db *sql.DB, noteID int64) (Document, error) {
	var doc Document
	doc.NoteID = noteID
	if err := db.QueryRow(`
		SELECT n.title, COALESCE(u.body, '') AS body
		FROM notes n
		LEFT JOIN updates u ON u.id = (
			SELECT id FROM updates WHERE note_id = n.id ORDER BY id DESC LIMIT 1
		)
		WHERE n.id = ?
	`, noteID).Scan(&doc.Title, &doc.Body); err != nil {
		return Document{}, err
	}

	segments, err := LoadPathSegments(db, noteID)
	if err != nil {
		return Document{}, err
	}
	doc.PathSegments = segments

	tags, err := loadTags(db, noteID)
	if err != nil {
		return Document{}, err
	}
	doc.Tags = tags

	return doc, nil
}

func LoadPathSegments(db *sql.DB, noteID int64) ([]string, error) {
	var chain []string
	cur := noteID
	for {
		var title string
		var parentID sql.NullInt64
		if err := db.QueryRow(`SELECT title, parent_id FROM notes WHERE id = ?`, cur).Scan(&title, &parentID); err != nil {
			return nil, err
		}
		title = strings.TrimSpace(title)
		if title == "" {
			title = "Untitled"
		}
		chain = append(chain, title)
		if !parentID.Valid {
			break
		}
		cur = parentID.Int64
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func BuildChunks(doc Document) []Chunk {
	chunks := make([]Chunk, 0, 4)
	title := strings.TrimSpace(doc.Title)
	if title != "" {
		chunks = append(chunks, Chunk{Field: ChunkFieldTitle, Ordinal: 0, Text: title})
	}

	pathText := strings.TrimSpace(doc.SearchPathText())
	if pathText != "" && !strings.EqualFold(pathText, title) {
		chunks = append(chunks, Chunk{Field: ChunkFieldPath, Ordinal: 0, Text: pathText})
	}

	tagText := strings.TrimSpace(strings.Join(cleanTags(doc.Tags), " "))
	if tagText != "" {
		chunks = append(chunks, Chunk{Field: ChunkFieldTags, Ordinal: 0, Text: tagText})
	}

	ordinal := 0
	for _, paragraph := range splitParagraphs(doc.Body) {
		for _, chunkText := range splitLongTextForEmbedding(paragraph) {
			chunks = append(chunks, Chunk{Field: ChunkFieldBody, Ordinal: ordinal, Text: chunkText})
			ordinal++
		}
	}

	return chunks
}

func ReplaceNoteIndex(db *sql.DB, embedder llm.Embedder, doc Document) (int, error) {
	release := llm.BeginBackendUse(embedder)
	defer release()

	chunks := BuildChunks(doc)
	indexed := make([]struct {
		Chunk
		VecJSON string
	}, 0, len(chunks))

	for _, chunk := range chunks {
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		vec, err := embedder.GenerateEmbedding(text)
		if err != nil {
			return 0, fmt.Errorf("embed %s chunk %d: %w", chunk.Field, chunk.Ordinal, err)
		}
		indexed = append(indexed, struct {
			Chunk
			VecJSON string
		}{
			Chunk:   chunk,
			VecJSON: llm.EmbeddingToJSON(vec),
		})
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if err := deleteNoteIndexTx(tx, doc.NoteID); err != nil {
		return 0, err
	}

	for _, item := range indexed {
		res, err := tx.Exec(
			`INSERT INTO `+ChunkTableName+`(note_id, field, ordinal, content) VALUES (?, ?, ?, ?)`,
			doc.NoteID,
			item.Field,
			item.Ordinal,
			item.Text,
		)
		if err != nil {
			return 0, fmt.Errorf("insert %s chunk %d: %w", item.Field, item.Ordinal, err)
		}
		chunkID, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("resolve chunk id: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO `+VecTableName+`(rowid, `+VecColumnName+`) VALUES (?, ?)`,
			chunkID,
			item.VecJSON,
		); err != nil {
			return 0, fmt.Errorf("insert vector for %s chunk %d: %w", item.Field, item.Ordinal, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(indexed), nil
}

func DeleteNoteIndex(db *sql.DB, noteID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteNoteIndexTx(tx, noteID); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteNoteIndexTx(tx *sql.Tx, noteID int64) error {
	if _, err := tx.Exec(
		`DELETE FROM `+VecTableName+` WHERE rowid IN (SELECT id FROM `+ChunkTableName+` WHERE note_id = ?)`,
		noteID,
	); err != nil {
		return fmt.Errorf("delete vectors: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM `+ChunkTableName+` WHERE note_id = ?`, noteID); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	return nil
}

func loadTags(db *sql.DB, noteID int64) ([]string, error) {
	rows, err := db.Query(`
		SELECT t.name
		FROM tags t
		JOIN tags_refs tr ON tr.tag_id = t.id
		WHERE tr.note_id = ?
		ORDER BY t.name
	`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, rows.Err()
}

func cleanTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func splitParagraphs(body string) []string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	parts := strings.Split(normalized, "\n\n")
	paragraphs := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		paragraphs = append(paragraphs, part)
	}
	return paragraphs
}

func splitLongTextForEmbedding(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if utf8.RuneCountInString(text) <= llm.MaxEmbeddingChars {
		return []string{text}
	}

	runes := []rune(text)
	chunks := make([]string, 0, len(runes)/llm.MaxEmbeddingChars+1)
	start := 0
	for start < len(runes) {
		end := start + llm.MaxEmbeddingChars
		if end >= len(runes) {
			chunk := strings.TrimSpace(string(runes[start:]))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		split := end
		for i := end; i > start+llm.MaxEmbeddingChars/2; i-- {
			if unicode.IsSpace(runes[i-1]) {
				split = i
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[start:split]))
		if chunk == "" {
			split = end
			chunk = strings.TrimSpace(string(runes[start:split]))
		}
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = split
		for start < len(runes) && unicode.IsSpace(runes[start]) {
			start++
		}
	}
	return chunks
}
