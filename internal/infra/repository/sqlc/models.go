// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0

package sqlc

import (
	"database/sql"
)

type User struct {
	ID          int64
	Username    string
	Password    string
	DisplayName string
	CreatedAt   sql.NullTime
	UpdatedAt   sql.NullTime
}