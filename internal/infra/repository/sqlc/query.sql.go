// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: query.sql

package sqlc

import (
	"context"
	"database/sql"
)

const createUser = `-- name: CreateUser :execresult
INSERT INTO user (username, password, display_name, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
`

type CreateUserParams struct {
	Username    string
	Password    string
	DisplayName string
	CreatedAt   sql.NullTime
	UpdatedAt   sql.NullTime
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (sql.Result, error) {
	return q.db.ExecContext(ctx, createUser,
		arg.Username,
		arg.Password,
		arg.DisplayName,
		arg.CreatedAt,
		arg.UpdatedAt,
	)
}

const deleteUser = `-- name: DeleteUser :exec
DELETE FROM user
WHERE id = ?
`

func (q *Queries) DeleteUser(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, deleteUser, id)
	return err
}

const getUser = `-- name: GetUser :one
SELECT id, username, password, display_name, created_at, updated_at
FROM user
WHERE username = ? LIMIT 1
`

func (q *Queries) GetUser(ctx context.Context, username string) (User, error) {
	row := q.db.QueryRowContext(ctx, getUser, username)
	var i User
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.Password,
		&i.DisplayName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const getUserById = `-- name: GetUser :one
SELECT id, username, password, display_name, created_at, updated_at
FROM user
WHERE id = ? LIMIT 1
`

func (q *Queries) GetUserById(ctx context.Context, id string) (User, error) {
	row := q.db.QueryRowContext(ctx, getUserById, id)
	var i User
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.Password,
		&i.DisplayName,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}
