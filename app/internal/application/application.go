package application

import "context"

type UseCase[C any, R any] interface {
	Execute(ctx context.Context, cmd C) (R, error)
}
