package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// ImageRepo persists image metadata and tag associations.
type ImageRepo struct {
	db *sql.DB
}

func NewImageRepo(d *sql.DB) *ImageRepo { return &ImageRepo{db: d} }

// UpsertImage inserts or updates an image row, returning its id.
func (r *ImageRepo) UpsertImage(filePath, prompt string) (int32, error) {
	res, err := r.db.Exec(
		`INSERT INTO images (file_path, prompt) VALUES (?, ?)
         ON CONFLICT(file_path) DO UPDATE SET prompt=excluded.prompt`,
		filePath, prompt)
	if err != nil {
		return 0, fmt.Errorf("upsert image: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return int32(id), nil
}

// GetImagePath returns the file path for an image id.
func (r *ImageRepo) GetImagePath(id int32) (string, error) {
	var p string
	err := r.db.QueryRow(`SELECT file_path FROM images WHERE id = ?`, id).Scan(&p)
	if err != nil {
		return "", fmt.Errorf("get image path: %w", err)
	}
	return p, nil
}

// GetImagePathPrompt returns the file path and stored prompt for an image id.
func (r *ImageRepo) GetImagePathPrompt(id int32) (string, string, error) {
	var p, pr string
	err := r.db.QueryRow(`SELECT file_path, prompt FROM images WHERE id = ?`, id).Scan(&p, &pr)
	if err != nil {
		return "", "", fmt.Errorf("get image: %w", err)
	}
	return p, pr, nil
}

// GetPromptByPath returns the currently stored prompt for a file path (before
// any upsert). The boolean reports whether a row already exists. Used by the
// scan pipeline to decide whether an already-embedded image must be
// re-embedded because its prompt text improved.
func (r *ImageRepo) GetPromptByPath(filePath string) (string, bool, error) {
	var p string
	err := r.db.QueryRow(`SELECT prompt FROM images WHERE file_path = ?`, filePath).Scan(&p)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get prompt by path: %w", err)
	}
	return p, true, nil
}

// SearchByText returns image ids whose stored prompt or file path contains q
// (case-insensitive substring match). This powers keyword search, which works
// without any embedding model. Results are ordered by rowid (insertion order)
// and capped at K.
func (r *ImageRepo) SearchByText(q string, K int) ([]int32, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	// Escape SQLite LIKE wildcards so the user query is matched literally.
	esc := strings.ReplaceAll(strings.ReplaceAll(q, `\`, `\\`), "%", `\%`)
	like := "%" + esc + "%"
	rows, err := r.db.Query(
		`SELECT id FROM images
          WHERE prompt LIKE ? ESCAPE '\'
             OR file_path LIKE ? ESCAPE '\'
          ORDER BY id
          LIMIT ?`,
		like, like, K)
	if err != nil {
		return nil, fmt.Errorf("search by text: %w", err)
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan text id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AddTag associates a tag (created if absent) with an image.
func (r *ImageRepo) AddTag(imageID int32, tag string) error {
	var tagID int32
	err := r.db.QueryRow(`SELECT id FROM tags WHERE name = ?`, tag).Scan(&tagID)
	if err == sql.ErrNoRows {
		res, insErr := r.db.Exec(`INSERT INTO tags (name) VALUES (?)`, tag)
		if insErr != nil {
			return fmt.Errorf("insert tag: %w", insErr)
		}
		tid, insErr := res.LastInsertId()
		if insErr != nil {
			return fmt.Errorf("tag id: %w", insErr)
		}
		tagID = int32(tid)
	} else if err != nil {
		return fmt.Errorf("select tag: %w", err)
	}
	_, err = r.db.Exec(
		`INSERT OR IGNORE INTO image_tags (image_id, tag_id) VALUES (?, ?)`,
		imageID, tagID)
	if err != nil {
		return fmt.Errorf("link tag: %w", err)
	}
	return nil
}

// GetByTag returns image ids that have the given tag.
func (r *ImageRepo) GetByTag(tag string) ([]int32, error) {
	rows, err := r.db.Query(
		`SELECT it.image_id FROM image_tags it
         JOIN tags t ON t.id = it.tag_id
         WHERE t.name = ?`, tag)
	if err != nil {
		return nil, fmt.Errorf("get by tag: %w", err)
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan tag id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
