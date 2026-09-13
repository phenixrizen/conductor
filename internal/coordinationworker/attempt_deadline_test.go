package coordinationworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/store"
)

type delayedAttemptStore struct {
	WorkStore
	mu               sync.Mutex
	work             store.CoordinationWork
	request          execution.Request
	attempt          *store.CoordinationAttempt
	entered, release chan struct{}
	receipt          *domain.TaskReceipt
}

func (s *delayedAttemptStore) CoordinationWork(context.Context, string, string) (store.CoordinationWork, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.work
	if s.receipt != nil {
		w.Run.Receipts = []domain.TaskReceipt{*s.receipt}
	}
	return w, nil
}
func (s *delayedAttemptStore) CoordinationAttempt(context.Context, string, string, string) (*store.CoordinationAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempt == nil {
		return nil, nil
	}
	a := *s.attempt
	a.RecoveryReady = !time.Now().Before(a.Deadline)
	return &a, nil
}
func (s *delayedAttemptStore) CoordinationInput(context.Context, string, string, string) (execution.Request, error) {
	return s.request, nil
}
func (s *delayedAttemptStore) BeginCoordinationAttempt(_ context.Context, id, binding, task, digest string) (*store.CoordinationAttempt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The database response is already near its recovery deadline, as can happen
	// when acknowledgment/authorization is delayed; no new timeout may restart it.
	s.attempt = &store.CoordinationAttempt{RunID: id, TaskID: task, InputDigest: digest, Deadline: time.Now().Add(100 * time.Millisecond)}
	a := *s.attempt
	return &a, true, nil
}
func (s *delayedAttemptStore) CheckCoordinationWork(context.Context, string, string) error {
	close(s.entered)
	<-s.release // Model an authorized response resumed after recovery completed.
	return nil
}
func (s *delayedAttemptStore) StopCoordinationTask(_ context.Context, _, _, task, outcome, _ string, _ bool) (domain.TaskReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.receipt == nil {
		s.receipt = &domain.TaskReceipt{TaskID: task, Digest: strings.Repeat("e", 64), Outcome: outcome}
	}
	return *s.receipt, nil
}
func TestDelayedAuthorizedResponseCannotLaunchAfterAttemptRecovery(t *testing.T) {
	profile := ProfileConfig{WorkspaceID: "team", ID: "fixture", Image: "sha256:" + strings.Repeat("a", 64), Profile: execution.Profile{Adapter: "command/v1", Command: []string{"/bin/true"}}}
	profileDigest, _ := domain.JSONDigest(profile.Profile)
	ref := coordinationworkflow.TaskReference{Run: coordinationworkflow.Reference{ID: strings.Repeat("b", 32), Binding: strings.Repeat("c", 32)}, TaskID: strings.Repeat("d", 32)}
	task := domain.CoordinationTask{ID: "work", Profile: profile.ID, ProfileDigest: profileDigest, Image: profile.Image, TimeoutSeconds: 1}
	request := execution.Request{RunID: ref.Run.ID, TaskID: ref.TaskID, ChangeID: "package", Revision: 1, Digest: strings.Repeat("a", 64), GraphDigest: strings.Repeat("b", 64), Repositories: []execution.Repository{{ID: "repo", Commit: strings.Repeat("c", 40), Bundle: []byte("synthetic never-executed source"), WritablePaths: []string{"source"}}}, Prompt: "synthetic", TimeoutSeconds: 1}
	s := &delayedAttemptStore{work: store.CoordinationWork{Run: domain.CoordinationRun{ID: ref.Run.ID, WorkspaceID: "team", Plan: domain.CoordinationPlan{Tasks: []domain.CoordinationTask{task}}}, TaskIDs: map[string]string{task.ID: ref.TaskID}}, request: request, entered: make(chan struct{}), release: make(chan struct{})}
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	marker := filepath.Join(dir, "started")
	// Only lifecycle RPCs are observed. No actual daemon, source or command executes.
	script := "#!/bin/sh\nif [ \"$1\" = run ] || [ \"$1\" = create ] || [ \"$1\" = start ]; then\n  : > '" + marker + "'\n  exit 1\nfi\nexit 0\n"
	if err := os.WriteFile(docker, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	a, err := NewActivity(s, []ProfileConfig{profile}, docker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := a.Execute(ctx, ref); done <- err }()
	select {
	case <-s.entered:
	case <-ctx.Done():
		t.Fatal("original attempt never reached delayed boundary")
	}
	time.Sleep(150 * time.Millisecond)
	recovered, err := a.Execute(ctx, ref)
	if err != nil || recovered.Outcome != "unresolved" {
		t.Fatal("redelivery did not confirm unresolved cleanup", err, recovered)
	}
	close(s.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("original activity started Docker after recovery confirmed cleanup")
	}
}
