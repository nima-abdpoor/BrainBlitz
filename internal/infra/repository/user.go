package repository

import (
	"BrainBlitz.com/game/internal/core/dto"
	"BrainBlitz.com/game/internal/core/port/repository"
	"errors"
	"strings"
)

const (
	insertUserStatement = "INSERT INTO User ( " +
		"`email`, " +
		"`password`, " +
		"`display_name`, " +
		"`created_at`," +
		"`updated_at`) " +
		"VALUES (?, ?, ?, ?, ?)"
)

const (
	duplicateEntryMsg = "Duplicate entry"
	numberRowInserted = 1
)

var (
	insertUserErr = errors.New("failed to insert user")
)

type userRepository struct {
	DB repository.Database
}

func NewUserRepository(db repository.Database) repository.UserRepository {
	return &userRepository{
		DB: db,
	}
}

func (ur userRepository) Insert(dto dto.UserDTO) error {
	result, err := ur.DB.GetDB().Exec(insertUserStatement,
		dto.Email,
		dto.HashedPassword,
		dto.DisplayName,
		dto.CreatedAt,
		dto.UpdatedAt,
	)

	if err != nil {
		if strings.Contains(err.Error(), duplicateEntryMsg) {
			return repository.DuplicateUser
		}
		return err
	}
	numRow, err := result.RowsAffected()

	if err != nil {
		return err
	}
	if numRow != numberRowInserted {
		return insertUserErr
	}
	return nil
}
