package store

import (
	"database/sql"
	"errors"
)

func maybeNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
