package setup

import "github.com/i5heu/ouroboros-db"

func StartDB(dataPath string) (*ouroboros.OuroborosDB, error) {
	config := ouroboros.Config{
		Paths: []string{dataPath},
	}
	db, err := ouroboros.NewOuroborosDB(config)
	if err != nil {
		return nil, err
	}
	return db, nil
}
