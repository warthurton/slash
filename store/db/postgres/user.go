package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/yourselfhosted/slash/store"
)

func (d *DB) CreateUser(ctx context.Context, create *store.User) (*store.User, error) {
	stmt := `
		INSERT INTO "user" (
			email,
			nickname,
			password_hash,
			role
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_ts, updated_ts, row_status
	`
	var rowStatus string
	if err := d.db.QueryRowContext(ctx, stmt,
		create.Email,
		create.Nickname,
		create.PasswordHash,
		create.Role,
	).Scan(
		&create.ID,
		&create.CreatedTs,
		&create.UpdatedTs,
		&rowStatus,
	); err != nil {
		return nil, err
	}

	user := create
	user.RowStatus = store.ConvertRowStatusStringToStorepb(rowStatus)
	return user, nil
}

func (d *DB) UpdateUser(ctx context.Context, update *store.UpdateUser) (*store.User, error) {
	set, args := []string{}, []any{}
	if v := update.RowStatus; v != nil {
		set, args = append(set, "row_status = "+placeholder(len(args)+1)), append(args, v.String())
	}
	if v := update.Email; v != nil {
		set, args = append(set, "email = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := update.Nickname; v != nil {
		set, args = append(set, "nickname = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := update.PasswordHash; v != nil {
		set, args = append(set, "password_hash = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := update.Role; v != nil {
		set, args = append(set, "role = "+placeholder(len(args)+1)), append(args, *v)
	}
	if len(set) == 0 {
		return nil, errors.New("no fields to update")
	}

	stmt := `
		UPDATE "user"
		SET ` + strings.Join(set, ", ") + `
		WHERE id = ` + placeholder(len(args)+1) + `
		RETURNING id, created_ts, updated_ts, row_status, email, nickname, password_hash, role
	`
	args = append(args, update.ID)
	user := &store.User{}
	var rowStatus string
	if err := d.db.QueryRowContext(ctx, stmt, args...).Scan(
		&user.ID,
		&user.CreatedTs,
		&user.UpdatedTs,
		&rowStatus,
		&user.Email,
		&user.Nickname,
		&user.PasswordHash,
		&user.Role,
	); err != nil {
		return nil, err
	}

	user.RowStatus = store.ConvertRowStatusStringToStorepb(rowStatus)
	return user, nil
}

func (d *DB) ListUsers(ctx context.Context, find *store.FindUser) ([]*store.User, error) {
	where, args := []string{"1 = 1"}, []any{}

	if v := find.ID; v != nil {
		where, args = append(where, "id = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := find.RowStatus; v != nil {
		where, args = append(where, "row_status = "+placeholder(len(args)+1)), append(args, v.String())
	}
	if v := find.Email; v != nil {
		where, args = append(where, "email = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := find.Nickname; v != nil {
		where, args = append(where, "nickname = "+placeholder(len(args)+1)), append(args, *v)
	}
	if v := find.Role; v != nil {
		where, args = append(where, "role = "+placeholder(len(args)+1)), append(args, *v)
	}

	query := `
		SELECT 
			id,
			created_ts,
			updated_ts,
			row_status,
			email,
			nickname,
			password_hash,
			role
		FROM "user"
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY updated_ts DESC, created_ts DESC
	`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]*store.User, 0)
	for rows.Next() {
		user := &store.User{}
		var rowStatus string
		if err := rows.Scan(
			&user.ID,
			&user.CreatedTs,
			&user.UpdatedTs,
			&rowStatus,
			&user.Email,
			&user.Nickname,
			&user.PasswordHash,
			&user.Role,
		); err != nil {
			return nil, err
		}
		user.RowStatus = store.ConvertRowStatusStringToStorepb(rowStatus)
		list = append(list, user)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return list, nil
}

func (d *DB) DeleteUser(ctx context.Context, delete *store.DeleteUser) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM "user" WHERE id = $1`, delete.ID); err != nil {
		return err
	}
	return nil
}
