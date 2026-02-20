package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
)

type contextKey string

const WorkerIDContextKey contextKey = "worker_id"

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("unauthorized worker token"))

func TokenAuthInterceptor(queries *sqlc.Queries) connect.UnaryInterceptorFunc {
	return TokenAuthInterceptorWithCache(queries, nil)
}

func TokenAuthInterceptorWithCache(queries *sqlc.Queries, cache *WorkerAuthCache) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			workerID, err := authenticateWorkerToken(ctx, queries, cache, req.Header().Get("Authorization"))
			if err != nil {
				return nil, err
			}

			ctx = context.WithValue(ctx, WorkerIDContextKey, workerID)
			return next(ctx, req)
		}
	}
}

func WorkerIDFromContext(ctx context.Context) (string, bool) {
	workerID, ok := ctx.Value(WorkerIDContextKey).(string)
	return workerID, ok
}

func TokenAuthHandler(queries *sqlc.Queries, next http.Handler) http.Handler {
	return TokenAuthHandlerWithCache(queries, nil, next)
}

func TokenAuthHandlerWithCache(queries *sqlc.Queries, cache *WorkerAuthCache, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerID, err := authenticateWorkerToken(r.Context(), queries, cache, r.Header.Get("Authorization"))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), WorkerIDContextKey, workerID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticateWorkerToken(ctx context.Context, queries *sqlc.Queries, cache *WorkerAuthCache, authHeader string) (string, error) {
	token, err := parseBearerToken(authHeader)
	if err != nil {
		return "", err
	}

	tokenHash := auth.HashToken(token)
	if cache != nil {
		if workerID, ok := cache.Get(tokenHash); ok {
			return workerID, nil
		}
	}

	worker, err := queries.GetWorkerByTokenHash(ctx, sql.NullString{String: tokenHash, Valid: true})
	if err != nil {
		return "", errUnauthenticated
	}

	if cache != nil {
		cache.Set(tokenHash, worker.ID)
	}

	return worker.ID, nil
}

func parseBearerToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errUnauthenticated
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", errUnauthenticated
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if !auth.ValidateTokenFormat(token) {
		return "", errUnauthenticated
	}

	return token, nil
}
