package srvtext

import "context"

// readerKey carries, on a request's context, the language of the person the
// response is FOR — so a layer that has no *http.Request (ops.Service's row
// decorator, wasmplugin.DecorateOps) can still say an app's words in it.
type readerKey struct{}

// WithReader returns ctx carrying lang as the reader's language. An empty
// lang leaves ctx as it is.
func WithReader(ctx context.Context, lang string) context.Context {
	if lang == "" {
		return ctx
	}
	return context.WithValue(ctx, readerKey{}, lang)
}

// Reader is the reader's language WithReader put on ctx, or "" when nobody
// said (a background caller: the worker, the scheduler).
func Reader(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(readerKey{}).(string)
	return s
}
