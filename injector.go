package fault

import (
	"errors"
	"math/rand"
	"net/http"
	"time"
)

var (
	// ErrInvalidHTTPCode returns when an invalid http status code is provided.
	ErrInvalidHTTPCode = errors.New("not a valid http status code")
)

type InjectorState int

const (
	StateStarted InjectorState = iota + 1
	StateFinished
	StateSkipped
)

// Injector is an interface for our fault injection middleware. Injectors are wrapped into Faults.
// Faults handle running the Injector the correct percent of the time.
type Injector interface {
	Name() string
	Handler(next http.Handler) http.Handler
	SetReporter(Reporter)
}

// ChainInjector combines many injectors into a single chain injector. In a chain injector the
// Handler func will execute ChainInjector.middlewares in order and then returns.
type ChainInjector struct {
	middlewares []func(next http.Handler) http.Handler
	reporter    Reporter
}

// NewChainInjector combines many injectors into a single chain injector. In a chain injector the
// Handler() for each injector will execute in the order provided.
func NewChainInjector(is ...Injector) (*ChainInjector, error) {
	chainInjector := &ChainInjector{}
	for _, i := range is {
		chainInjector.middlewares = append(chainInjector.middlewares, i.Handler)
	}

	return chainInjector, nil
}

// Name returns the name of the Injector
func (i *ChainInjector) Name() string {
	return "Chain Injector"
}

// Handler executes ChainInjector.middlewares in order and then returns.
func (i *ChainInjector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i != nil {
			r = updateRequestContextValue(r, ContextValueChainInjector)

			// Loop in reverse to preserve handler order
			for idx := len(i.middlewares) - 1; idx >= 0; idx-- {
				next = i.middlewares[idx](next)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetReporter sets the Reporter for the injector
func (i *ChainInjector) SetReporter(r Reporter) {
	i.reporter = r
}

// RandomInjector combines many injectors into a single injector. When the random injector is called
// it randomly runs one of the provided injectors.
type RandomInjector struct {
	middlewares []func(next http.Handler) http.Handler
	reporter    Reporter

	rand  *rand.Rand
	randF func(int) int
}

// NewRandomInjector combines many injectors into a single random injector. When the random injector
// is called it randomly runs one of the provided injectors.
func NewRandomInjector(is ...Injector) (*RandomInjector, error) {
	RandomInjector := &RandomInjector{}

	RandomInjector.rand = rand.New(rand.NewSource(defaultRandSeed))
	RandomInjector.randF = RandomInjector.rand.Intn

	for _, i := range is {
		RandomInjector.middlewares = append(RandomInjector.middlewares, i.Handler)
	}

	return RandomInjector, nil
}

// Name returns the name of the Injector
func (i *RandomInjector) Name() string {
	return "Random Injector"
}

// SetRandSeed sets the random seed for RandomInjector to a non-default value
func (i *RandomInjector) SetRandSeed(s int64) {
	i.rand = rand.New(rand.NewSource(s))
	i.randF = i.rand.Intn
}

// Handler executes a random injector from RandomInjector.middlewares
func (i *RandomInjector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i != nil && len(i.middlewares) > 0 {
			r = updateRequestContextValue(r, ContextValueRandomInjector)
			i.middlewares[i.randF(len(i.middlewares))](next).ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// SetReporter sets the Reporter for the injector
func (i *RandomInjector) SetReporter(r Reporter) {
	i.reporter = r
}

// RejectInjector immediately sends back an empty response.
type RejectInjector struct {
	reporter Reporter
}

// NewRejectInjector returns a RejectInjector struct.
func NewRejectInjector() (*RejectInjector, error) {
	return &RejectInjector{}, nil
}

// Name returns the name of the Injector
func (i *RejectInjector) Name() string {
	return "Reject Injector"
}

// Handler immediately rejects the request, returning an empty response.
func (i *RejectInjector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i != nil {
			reportWithMessage(i.reporter, i.Name(), StateStarted)
		}

		// This is a specialized and documented way of sending an interrupted response to
		// the client without printing the panic stack trace or erroring.
		// https://golang.org/pkg/net/http/#Handler
		panic(http.ErrAbortHandler)
	})
}

// SetReporter sets the Reporter for the injector
func (i *RejectInjector) SetReporter(r Reporter) {
	i.reporter = r
}

// ErrorInjector immediately responds with an http status code and the error message associated with
// that code.
type ErrorInjector struct {
	statusCode int
	statusText string
	reporter   Reporter
}

// NewErrorInjector returns an ErrorInjector that reponds with the configured status code.
func NewErrorInjector(code int) (*ErrorInjector, error) {
	statusText := http.StatusText(code)
	if statusText == "" {
		return nil, ErrInvalidHTTPCode
	}

	return &ErrorInjector{
		statusCode: code,
		statusText: statusText,
	}, nil
}

// Name returns the name of the Injector
func (i *ErrorInjector) Name() string {
	return "Error Injector"
}

// Handler immediately responds with the configured HTTP status code and default status text for
// that code.
func (i *ErrorInjector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i != nil {
			reportWithMessage(i.reporter, i.Name(), StateStarted)

			if http.StatusText(i.statusCode) != "" {
				http.Error(w, i.statusText, i.statusCode)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetReporter sets the Reporter for the injector
func (i *ErrorInjector) SetReporter(r Reporter) {
	i.reporter = r
}

// SlowInjector sleeps a specified duration and then continues the request. Simulates latency.
type SlowInjector struct {
	duration time.Duration
	reporter Reporter
	sleep    func(t time.Duration)
}

// NewSlowInjector returns a SlowInjector that adds the configured latency.
func NewSlowInjector(d time.Duration) (*SlowInjector, error) {
	return &SlowInjector{
		duration: d,
		sleep:    time.Sleep,
	}, nil
}

// Name returns the name of the Injector
func (i *SlowInjector) Name() string {
	return "Slow Injector"
}

// Handler waits the configured duration and then continues the request.
func (i *SlowInjector) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i != nil && i.sleep != nil {
			reportWithMessage(i.reporter, i.Name(), StateStarted)
			i.sleep(i.duration)
			reportWithMessage(i.reporter, i.Name(), StateFinished)

			next.ServeHTTP(w, updateRequestContextValue(r, ContextValueSlowInjector))
		} else {
			next.ServeHTTP(w, updateRequestContextValue(r, ContextValueSkipped))
		}
	})
}

// SetReporter sets the Reporter for the injector
func (i *SlowInjector) SetReporter(r Reporter) {
	i.reporter = r
}
