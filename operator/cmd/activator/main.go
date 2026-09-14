// Command activator serves requests for sleeping apps.
//
// One runs per namespace that contains an app with wake-on-traffic enabled. The operator
// creates and removes it, and points a sleeping app's Ingress at it.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kubernetes.getvesta.sh/operator/activator"
)

func main() {
	namespace := os.Getenv("VESTA_ACTIVATOR_NAMESPACE")
	if namespace == "" {
		log.Fatal("VESTA_ACTIVATOR_NAMESPACE is required: an activator serves one namespace")
	}

	waker, err := activator.NewKubeWaker(namespace)
	if err != nil {
		log.Fatalf("cannot reach the Kubernetes API: %v", err)
	}

	a := activator.New(waker, activator.Options{Namespace: namespace})

	// Application traffic. Every path on this port belongs to the app.
	srv := &http.Server{
		Addr:    ":8080",
		Handler: a.Handler(),
		// No write timeout: a held request is the entire point, and the activator bounds it
		// itself with WakeTimeout. A server-level deadline here would cut the wait short
		// and return a bare error in place of the app's response.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// The activator's own health, on its own port, so no app path is reserved.
	probe := &http.Server{
		Addr:              ":8081",
		Handler:           activator.ProbeHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("[activator] serving %s on :8080", namespace)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serving: %v", err)
		}
	}()

	go func() {
		if err := probe.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[activator] health endpoint stopped: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = probe.Shutdown(ctx)
}
