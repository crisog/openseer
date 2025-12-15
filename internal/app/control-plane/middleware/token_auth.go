package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
)

type contextKey string

const WorkerIDContextKey contextKey = "worker_id"

func TokenAuthInterceptor(queries *sqlc.Queries) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			authHeader := req.Header().Get("Authorization")
			if authHeader == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}

			if !strings.HasPrefix(authHeader, "Bearer ") {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if !auth.ValidateTokenFormat(token) {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}

			tokenHash := auth.HashToken(token)
			worker, err := queries.GetWorkerByTokenHash(ctx, sql.NullString{String: tokenHash, Valid: true})
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, nil)
			}

			ctx = context.WithValue(ctx, WorkerIDContextKey, worker.ID)
			return next(ctx, req)
		}
	}
}

func WorkerIDFromContext(ctx context.Context) (string, bool) {
	workerID, ok := ctx.Value(WorkerIDContextKey).(string)
	return workerID, ok
}

func TokenAuthHandler(queries *sqlc.Queries, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !auth.ValidateTokenFormat(token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		tokenHash := auth.HashToken(token)
		worker, err := queries.GetWorkerByTokenHash(r.Context(), sql.NullString{String: tokenHash, Valid: true})
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), WorkerIDContextKey, worker.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
