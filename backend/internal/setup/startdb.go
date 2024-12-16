package setup

import (
	"fmt"
	"time"

	"github.com/i5heu/ouroboros-db"
)

func StartDB(absPath string) (*ouroboros.OuroborosDB, error) {
	fmt.Println("Starting database at path:", absPath)
	conf := ouroboros.Config{
		Paths:                     []string{absPath},
		MinimumFreeGB:             1,
		GarbageCollectionInterval: 10 * time.Minute,
	}

	// Create a new database instance
	db, err := ouroboros.NewOuroborosDB(conf)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %v", err)
	}

	return db, nil
}
