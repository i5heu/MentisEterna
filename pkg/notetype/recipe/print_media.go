package recipe

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	dbwrap "github.com/i5heu/MentisEterna/internal/db"
	"github.com/i5heu/MentisEterna/internal/media"
	"github.com/i5heu/MentisEterna/pkg/printer"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

func LoadNoteImage(ctx context.Context, sqlDB *sql.DB, fileID int64) (image.Image, error) {
	cfg, err := media.LoadConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("recipe: media config unavailable for image printing: %w", err)
	}
	service := media.NewService(&dbwrap.DB{DB: sqlDB}, cfg)

	var plaintext bytes.Buffer
	if _, err := service.ReadFile(ctx, fileID, &plaintext); err != nil {
		return nil, fmt.Errorf("recipe: read image file %d: %w", fileID, err)
	}

	img, _, err := image.Decode(bytes.NewReader(plaintext.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("recipe: decode image file %d: %w", fileID, err)
	}
	return img, nil
}

func PrintNoteImage(ctx context.Context, b *printer.Buf, sqlDB *sql.DB, fileID int64) error {
	img, err := LoadNoteImage(ctx, sqlDB, fileID)
	if err != nil {
		return err
	}
	return b.ImageBitColumn(img)
}
