package handler_test

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/saitddundar/vinctum-core/services/identity/repository"
)

// fakeQueries is a test double for repository.Queries backed by an in-memory map.
type fakeQueries struct {
	users map[string]repository.User
}

func newFakeQueries() *fakeQueries {
	return &fakeQueries{users: make(map[string]repository.User)}
}

func (f *fakeQueries) CreateUser(ctx context.Context, arg repository.CreateUserParams) (repository.User, error) {
	for _, u := range f.users {
		if u.Email == arg.Email {
			return repository.User{}, fmt.Errorf("duplicate")
		}
	}
	u := repository.User{
		ID:           "test-id-" + arg.Email,
		Username:     arg.Username,
		Email:        arg.Email,
		PasswordHash: arg.PasswordHash,
		CreatedAt:    time.Now(),
	}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeQueries) GetUserByEmail(ctx context.Context, email string) (repository.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return repository.User{}, pgx.ErrNoRows
}

func (f *fakeQueries) GetUserByID(ctx context.Context, id string) (repository.User, error) {
	u, ok := f.users[id]
	if !ok {
		return repository.User{}, pgx.ErrNoRows
	}
	return u, nil
}
