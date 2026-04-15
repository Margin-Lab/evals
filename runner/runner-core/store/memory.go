package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

type MemoryStore struct {
	mu sync.Mutex

	runs      map[string]*Run
	runOrder  []string
	instances map[string]*Instance

	instanceIDsByRun map[string][]string
	attemptsByID     map[string]*Attempt
	attemptIDsByInst map[string][]string
	leasesByInstance map[string]*lease

	runEventsByRun       map[string][]RunEvent
	instanceEventsByInst map[string][]InstanceEvent
	instanceResultsByID  map[string]StoredInstanceResult
	artifactsByID        map[string]Artifact
	artifactIDsByInst    map[string][]string

	eventSeq int64
	idSeq    int64
}

type lease struct {
	AttemptID     string
	LeaseToken    string
	WorkerID      string
	LeaseExpires  time.Time
	LastHeartbeat time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:                 map[string]*Run{},
		runOrder:             []string{},
		instances:            map[string]*Instance{},
		instanceIDsByRun:     map[string][]string{},
		attemptsByID:         map[string]*Attempt{},
		attemptIDsByInst:     map[string][]string{},
		leasesByInstance:     map[string]*lease{},
		runEventsByRun:       map[string][]RunEvent{},
		instanceEventsByInst: map[string][]InstanceEvent{},
		instanceResultsByID:  map[string]StoredInstanceResult{},
		artifactsByID:        map[string]Artifact{},
		artifactIDsByInst:    map[string][]string{},
	}
}

func (s *MemoryStore) CreateRun(_ context.Context, in CreateRunInput) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(in.RunID) == "" {
		return Run{}, fmt.Errorf("run id is required")
	}
	if _, ok := s.runs[in.RunID]; ok {
		return Run{}, fmt.Errorf("run already exists")
	}
	if strings.TrimSpace(in.ProjectID) == "" {
		return Run{}, fmt.Errorf("project id is required")
	}
	if in.At.IsZero() {
		in.At = time.Now().UTC()
	}
	if err := runbundle.Validate(in.Bundle); err != nil {
		return Run{}, fmt.Errorf("validate run bundle: %w", err)
	}
	if strings.TrimSpace(in.BundleHash) == "" {
		h, err := runbundle.HashSHA256(in.Bundle)
		if err != nil {
			return Run{}, err
		}
		in.BundleHash = h
	}

	run := &Run{
		RunID:           in.RunID,
		ProjectID:       in.ProjectID,
		CreatedByUser:   in.CreatedByUser,
		Name:            coalesce(in.Name, in.Bundle.ResolvedSnapshot.Name),
		State:           domain.RunStateQueued,
		SourceKind:      in.SourceKind,
		BundleHash:      in.BundleHash,
		Bundle:          in.Bundle,
		CancelRequested: false,
		CreatedAt:       in.At,
	}
	s.runs[run.RunID] = run
	s.runOrder = append(s.runOrder, run.RunID)

	for i, c := range in.Bundle.ResolvedSnapshot.Cases {
		id := fmt.Sprintf("%s-inst-%04d", run.RunID, i+1)
		inst := &Instance{
			InstanceID: id,
			RunID:      run.RunID,
			Ordinal:    i,
			Case:       c,
			State:      domain.InstanceStatePending,
			CreatedAt:  in.At,
			UpdatedAt:  in.At,
		}
		s.instances[id] = inst
		s.instanceIDsByRun[run.RunID] = append(s.instanceIDsByRun[run.RunID], id)
	}

	s.emitRunEventLocked(run.RunID, "api", nil, domain.RunStateQueued, nil, in.At)
	s.refreshRunLocked(run.RunID, in.At)
	return s.cloneRunLocked(*run, false), nil
}

func (s *MemoryStore) RerunExact(_ context.Context, sourceRunID, newRunID, createdByUser, newName string, at time.Time) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src, ok := s.runs[sourceRunID]
	if !ok {
		return Run{}, ErrNotFound
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	bundle := runbundle.CloneForRerunExact(src.Bundle, "bun-"+newRunID, at, sourceRunID)
	h, err := runbundle.HashSHA256(bundle)
	if err != nil {
		return Run{}, err
	}
	return s.createRunLocked(CreateRunInput{
		RunID:         newRunID,
		ProjectID:     src.ProjectID,
		CreatedByUser: createdByUser,
		Name:          coalesce(newName, src.Name),
		SourceKind:    runbundle.SourceKindRunSnapshot,
		Bundle:        bundle,
		BundleHash:    h,
		At:            at,
	})
}

func (s *MemoryStore) createRunLocked(in CreateRunInput) (Run, error) {
	if _, exists := s.runs[in.RunID]; exists {
		return Run{}, fmt.Errorf("run already exists")
	}
	if err := runbundle.Validate(in.Bundle); err != nil {
		return Run{}, err
	}
	run := &Run{
		RunID:         in.RunID,
		ProjectID:     in.ProjectID,
		CreatedByUser: in.CreatedByUser,
		Name:          coalesce(in.Name, in.Bundle.ResolvedSnapshot.Name),
		State:         domain.RunStateQueued,
		SourceKind:    in.SourceKind,
		BundleHash:    in.BundleHash,
		Bundle:        in.Bundle,
		CreatedAt:     in.At,
	}
	s.runs[run.RunID] = run
	s.runOrder = append(s.runOrder, run.RunID)
	for i, c := range in.Bundle.ResolvedSnapshot.Cases {
		id := fmt.Sprintf("%s-inst-%04d", run.RunID, i+1)
		inst := &Instance{InstanceID: id, RunID: run.RunID, Ordinal: i, Case: c, State: domain.InstanceStatePending, CreatedAt: in.At, UpdatedAt: in.At}
		s.instances[id] = inst
		s.instanceIDsByRun[run.RunID] = append(s.instanceIDsByRun[run.RunID], id)
	}
	s.emitRunEventLocked(run.RunID, "api", nil, domain.RunStateQueued, nil, in.At)
	s.refreshRunLocked(run.RunID, in.At)
	return s.cloneRunLocked(*run, false), nil
}

func (s *MemoryStore) ListRuns(_ context.Context, projectID string, filter ListRunsFilter) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Run, 0, len(s.runOrder))
	for i := len(s.runOrder) - 1; i >= 0; i-- {
		r := s.runs[s.runOrder[i]]
		if r.ProjectID != projectID {
			continue
		}
		if filter.State != nil && r.State != *filter.State {
			continue
		}
		if filter.SourceKind != nil && r.SourceKind != *filter.SourceKind {
			continue
		}
		if filter.CreatedByUserID != nil && r.CreatedByUser != *filter.CreatedByUserID {
			continue
		}
		s.refreshRunLocked(r.RunID, time.Now().UTC())
		out = append(out, s.cloneRunLocked(*r, false))
	}
	return out, nil
}

func (s *MemoryStore) GetRun(_ context.Context, runID string, includeBundle bool) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.runs[runID]
	if !ok {
		return Run{}, ErrNotFound
	}
	s.refreshRunLocked(runID, time.Now().UTC())
	return s.cloneRunLocked(*r, includeBundle), nil
}

func (s *MemoryStore) CancelRun(_ context.Context, runID, reasonCode, reasonMessage string, at time.Time) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.runs[runID]
	if !ok {
		return Run{}, ErrNotFound
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	r.CancelRequested = true
	old := r.State
	next := domain.NextRunState(old, s.computeCountsLocked(runID), true)
	if next != old {
		r.State = next
		details := map[string]any{}
		if reasonCode != "" {
			details["reason_code"] = reasonCode
		}
		if reasonMessage != "" {
			details["reason_message"] = reasonMessage
		}
		s.emitRunEventLocked(runID, "api", &old, next, details, at)
	}
	if next.IsTerminal() && r.EndedAt == nil {
		t := at
		r.EndedAt = &t
	}
	s.refreshRunLocked(runID, at)
	return s.cloneRunLocked(*r, false), nil
}

func (s *MemoryStore) ListInstances(_ context.Context, runID string, state *domain.InstanceState) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids, ok := s.instanceIDsByRun[runID]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]Instance, 0, len(ids))
	for _, id := range ids {
		inst := s.instances[id]
		if state != nil && inst.State != *state {
			continue
		}
		out = append(out, *inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal < out[j].Ordinal })
	return out, nil
}

func (s *MemoryStore) GetInstance(_ context.Context, instanceID string) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[instanceID]
	if !ok {
		return Instance{}, ErrNotFound
	}
	return *inst, nil
}

func (s *MemoryStore) ListInstanceAttempts(_ context.Context, instanceID string) ([]Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.attemptIDsByInst[instanceID]
	out := make([]Attempt, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.attemptsByID[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ListRunEvents(_ context.Context, runID string) ([]RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.runEventsByRun[runID]
	out := make([]RunEvent, len(events))
	copy(out, events)
	return out, nil
}

func (s *MemoryStore) ListInstanceEvents(_ context.Context, instanceID string) ([]InstanceEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.instanceEventsByInst[instanceID]
	out := make([]InstanceEvent, len(events))
	copy(out, events)
	return out, nil
}

func (s *MemoryStore) GetInstanceResult(_ context.Context, instanceID string) (StoredInstanceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.instanceResultsByID[instanceID]
	if !ok {
		return StoredInstanceResult{}, ErrNotFound
	}
	result.Usage = usage.Clone(result.Usage)
	return result, nil
}

func (s *MemoryStore) ListInstanceResults(_ context.Context, runID string) ([]StoredInstanceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := s.instanceIDsByRun[runID]
	out := make([]StoredInstanceResult, 0, len(ids))
	for _, instanceID := range ids {
		result, ok := s.instanceResultsByID[instanceID]
		if !ok {
			continue
		}
		result.Usage = usage.Clone(result.Usage)
		out = append(out, result)
	}
	return out, nil
}

func (s *MemoryStore) ListArtifacts(_ context.Context, instanceID string) ([]Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.artifactIDsByInst[instanceID]
	out := make([]Artifact, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.artifactsByID[id])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Role == out[j].Role {
			return out[i].Ordinal < out[j].Ordinal
		}
		return out[i].Role < out[j].Role
	})
	return out, nil
}

func (s *MemoryStore) GetArtifact(_ context.Context, artifactID string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.artifactsByID[artifactID]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return a, nil
}

func (s *MemoryStore) ClaimPendingInstance(_ context.Context, workerID string, leaseDuration time.Duration, at time.Time) (ClaimedWork, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if workerID == "" {
		return ClaimedWork{}, false, fmt.Errorf("worker id is required")
	}
	if leaseDuration <= 0 {
		return ClaimedWork{}, false, fmt.Errorf("lease duration must be > 0")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	for _, runID := range s.runOrder {
		r := s.runs[runID]
		if r.CancelRequested {
			continue
		}
		if r.State != domain.RunStateQueued && r.State != domain.RunStateRunning {
			continue
		}
		counts := s.computeCountsLocked(runID)
		if counts.Running >= r.Bundle.ResolvedSnapshot.Execution.MaxConcurrency {
			continue
		}
		for _, instID := range s.instanceIDsByRun[runID] {
			inst := s.instances[instID]
			if inst.State != domain.InstanceStatePending {
				continue
			}
			attemptID := s.nextIDLocked("att")
			leaseToken := s.nextIDLocked("lease")
			attempt := &Attempt{
				AttemptID:       attemptID,
				InstanceID:      instID,
				WorkerID:        workerID,
				LeaseToken:      leaseToken,
				CreatedAt:       at,
				LastHeartbeatAt: at,
				LeaseExpiresAt:  at.Add(leaseDuration),
			}
			s.attemptsByID[attemptID] = attempt
			s.attemptIDsByInst[instID] = append(s.attemptIDsByInst[instID], attemptID)
			s.leasesByInstance[instID] = &lease{AttemptID: attemptID, LeaseToken: leaseToken, WorkerID: workerID, LeaseExpires: at.Add(leaseDuration), LastHeartbeat: at}

			oldInstState := inst.State
			inst.State = domain.InstanceStateProvisioning
			inst.UpdatedAt = at
			s.emitInstanceEventLocked(instID, attemptID, "worker", &oldInstState, inst.State, nil, at)

			if r.State == domain.RunStateQueued {
				oldRunState := r.State
				r.State = domain.RunStateRunning
				started := at
				r.StartedAt = &started
				s.emitRunEventLocked(runID, "worker", &oldRunState, r.State, nil, at)
			}
			s.refreshRunLocked(runID, at)
			return ClaimedWork{AttemptID: attemptID, LeaseToken: leaseToken, Run: s.cloneRunLocked(*r, true), Instance: *inst}, true, nil
		}
	}
	return ClaimedWork{}, false, nil
}

func (s *MemoryStore) UpdateInstanceState(_ context.Context, runID, instanceID, attemptID string, state domain.InstanceState, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[instanceID]
	if !ok || inst.RunID != runID {
		return ErrNotFound
	}
	if err := s.assertLeaseLocked(instanceID, attemptID, "", ""); err != nil {
		return err
	}
	if inst.State.IsTerminal() {
		return nil
	}
	if !domain.ValidInstanceTransition(inst.State, state) {
		return fmt.Errorf("invalid instance transition %q -> %q", inst.State, state)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	old := inst.State
	inst.State = state
	inst.UpdatedAt = at
	s.emitInstanceEventLocked(instanceID, attemptID, "worker", &old, state, nil, at)
	s.refreshRunLocked(runID, at)
	return nil
}

func (s *MemoryStore) UpdateInstanceImage(_ context.Context, runID, instanceID, attemptID, image string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[instanceID]
	if !ok || inst.RunID != runID {
		return ErrNotFound
	}
	if err := s.assertLeaseLocked(instanceID, attemptID, "", ""); err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if inst.State.IsTerminal() {
		return nil
	}
	resolved := strings.TrimSpace(image)
	if !runbundle.IsPinnedImageRef(resolved) {
		return fmt.Errorf("image must be pinned by digest (repo@sha256:... or sha256:...)")
	}
	if inst.Case.Image == resolved {
		return nil
	}
	inst.Case.Image = resolved
	inst.UpdatedAt = at
	return nil
}

func (s *MemoryStore) HeartbeatAttempt(_ context.Context, instanceID, attemptID, leaseToken, workerID string, leaseDuration time.Duration, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := s.assertLeaseLocked(instanceID, attemptID, leaseToken, workerID); err != nil {
		return err
	}
	ls := s.leasesByInstance[instanceID]
	ls.LastHeartbeat = at
	ls.LeaseExpires = at.Add(leaseDuration)
	att := s.attemptsByID[attemptID]
	att.LastHeartbeatAt = at
	att.LeaseExpiresAt = at.Add(leaseDuration)
	return nil
}

func (s *MemoryStore) FinalizeAttempt(_ context.Context, in FinalizeInput, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[in.InstanceID]
	if !ok || inst.RunID != in.RunID {
		return ErrNotFound
	}
	if err := s.assertLeaseLocked(in.InstanceID, in.AttemptID, "", ""); err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	final := in.Result.FinalState
	if !final.IsTerminal() {
		final = domain.InstanceStateInfraFailed
	}
	if !domain.ValidInstanceTransition(inst.State, final) {
		// In finalization we allow forced terminalization.
		final = domain.InstanceStateInfraFailed
	}
	old := inst.State
	inst.State = final
	inst.UpdatedAt = at
	s.emitInstanceEventLocked(in.InstanceID, in.AttemptID, "worker", &old, final, map[string]any{
		"provider_ref":  in.ProviderRef,
		"error_code":    in.Result.ErrorCode,
		"error_message": in.Result.ErrorMessage,
	}, at)

	if att, ok := s.attemptsByID[in.AttemptID]; ok {
		ended := at
		att.EndedAt = &ended
	}
	delete(s.leasesByInstance, in.InstanceID)
	s.instanceResultsByID[in.InstanceID] = buildStoredResult(in.InstanceID, in.AttemptID, in.ProviderRef, in.Result, final, at)
	s.storeArtifactsLocked(in.RunID, in.InstanceID, in.AttemptID, in.Artifacts, at)

	s.refreshRunLocked(in.RunID, at)
	return nil
}

func (s *MemoryStore) RequeueInfraFailure(_ context.Context, in RequeueInfraFailureInput, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[in.InstanceID]
	if !ok || inst.RunID != in.RunID {
		return false, ErrNotFound
	}
	if err := s.assertLeaseLocked(in.InstanceID, in.AttemptID, "", ""); err != nil {
		return false, err
	}
	if !in.Result.FinalState.IsInfraFailure() || in.MaxRetryCount <= 0 {
		return false, nil
	}
	usedRetries := s.retryCountLocked(in.InstanceID)
	if usedRetries >= in.MaxRetryCount {
		return false, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	if att, ok := s.attemptsByID[in.AttemptID]; ok && att.EndedAt == nil {
		ended := at
		att.EndedAt = &ended
	}
	delete(s.leasesByInstance, in.InstanceID)
	s.storeArtifactsLocked(in.RunID, in.InstanceID, in.AttemptID, in.Artifacts, at)

	old := inst.State
	inst.State = domain.InstanceStatePending
	inst.UpdatedAt = at
	retryIndex := usedRetries + 1
	s.emitInstanceEventLocked(in.InstanceID, in.AttemptID, "retry", &old, inst.State, map[string]any{
		"attempt_final_state": string(in.Result.FinalState),
		"retry_index":         retryIndex,
		"max_retry_count":     in.MaxRetryCount,
		"provider_ref":        in.ProviderRef,
		"error_code":          in.Result.ErrorCode,
		"error_message":       in.Result.ErrorMessage,
	}, at)
	s.refreshRunLocked(in.RunID, at)
	return true, nil
}

func (s *MemoryStore) CarryForwardInstance(_ context.Context, in CarryForwardInput, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.instances[in.InstanceID]
	if !ok || inst.RunID != in.RunID {
		return ErrNotFound
	}
	if inst.State.IsTerminal() {
		return nil
	}
	if inst.State != domain.InstanceStatePending {
		return fmt.Errorf("carry-forward requires pending instance, got %s", inst.State)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	final := in.Result.FinalState
	if !final.IsTerminal() {
		return fmt.Errorf("carry-forward result final state must be terminal")
	}
	attemptID := s.nextIDLocked("att_resume")
	attempt := &Attempt{
		AttemptID:       attemptID,
		InstanceID:      in.InstanceID,
		WorkerID:        "resume",
		LeaseToken:      "",
		CreatedAt:       at,
		LastHeartbeatAt: at,
		LeaseExpiresAt:  at,
	}
	ended := at
	attempt.EndedAt = &ended
	s.attemptsByID[attemptID] = attempt
	s.attemptIDsByInst[in.InstanceID] = append(s.attemptIDsByInst[in.InstanceID], attemptID)

	old := inst.State
	inst.State = final
	inst.UpdatedAt = at
	s.emitInstanceEventLocked(in.InstanceID, attemptID, "resume", &old, final, map[string]any{
		"source_run_id":      in.SourceRunID,
		"source_instance_id": in.SourceInstanceID,
		"carried_forward":    true,
		"provider_ref":       in.ProviderRef,
		"error_code":         in.Result.ErrorCode,
		"error_message":      in.Result.ErrorMessage,
	}, at)

	s.instanceResultsByID[in.InstanceID] = buildStoredResult(in.InstanceID, attemptID, in.ProviderRef, in.Result, final, at)
	s.storeArtifactsLocked(in.RunID, in.InstanceID, attemptID, in.Artifacts, at)
	s.refreshRunLocked(in.RunID, at)
	return nil
}

func (s *MemoryStore) RunCancelRequested(_ context.Context, runID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false, ErrNotFound
	}
	return r.CancelRequested, nil
}

func (s *MemoryStore) SweepCancelingRuns(_ context.Context, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if at.IsZero() {
		at = time.Now().UTC()
	}
	updated := 0
	for _, runID := range s.runOrder {
		r := s.runs[runID]
		if !r.CancelRequested {
			continue
		}
		for _, instanceID := range s.instanceIDsByRun[runID] {
			inst := s.instances[instanceID]
			if inst == nil || inst.State != domain.InstanceStatePending {
				continue
			}
			old := inst.State
			inst.State = domain.InstanceStateCanceled
			inst.UpdatedAt = at
			s.emitInstanceEventLocked(instanceID, "", "sweeper", &old, inst.State, map[string]any{"reason": "run_canceled"}, at)
		}
		before := r.State
		after := domain.NextRunState(before, s.computeCountsLocked(runID), true)
		if after != before {
			r.State = after
			s.emitRunEventLocked(runID, "sweeper", &before, after, nil, at)
			updated++
		}
		if after.IsTerminal() && r.EndedAt == nil {
			t := at
			r.EndedAt = &t
		}
	}
	return updated, nil
}

func (s *MemoryStore) ReapExpiredLeases(_ context.Context, at time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 {
		limit = 200
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	reaped := 0
	for instanceID, ls := range s.leasesByInstance {
		if reaped >= limit {
			break
		}
		if ls.LeaseExpires.After(at) {
			continue
		}
		inst := s.instances[instanceID]
		if inst == nil || inst.State.IsTerminal() {
			delete(s.leasesByInstance, instanceID)
			continue
		}
		if att := s.attemptsByID[ls.AttemptID]; att != nil && att.EndedAt == nil {
			ended := at
			att.EndedAt = &ended
		}
		delete(s.leasesByInstance, instanceID)
		old := inst.State
		inst.State = domain.InstanceStatePending
		inst.UpdatedAt = at
		s.emitInstanceEventLocked(instanceID, ls.AttemptID, "reaper", &old, inst.State, map[string]any{"reason": "lease_expired"}, at)
		s.refreshRunLocked(inst.RunID, at)
		reaped++
	}
	return reaped, nil
}

func (s *MemoryStore) assertLeaseLocked(instanceID, attemptID, leaseToken, workerID string) error {
	ls, ok := s.leasesByInstance[instanceID]
	if !ok {
		return ErrLeaseLost
	}
	if ls.AttemptID != attemptID {
		return ErrLeaseLost
	}
	if leaseToken != "" && ls.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	if workerID != "" && ls.WorkerID != workerID {
		return ErrLeaseLost
	}
	return nil
}

func (s *MemoryStore) emitRunEventLocked(runID, source string, from *domain.RunState, to domain.RunState, details map[string]any, at time.Time) {
	s.eventSeq++
	ev := RunEvent{EventID: s.eventSeq, RunID: runID, Source: source, FromState: from, ToState: to, Details: details, CreatedAt: at}
	s.runEventsByRun[runID] = append(s.runEventsByRun[runID], ev)
}

func (s *MemoryStore) emitInstanceEventLocked(instanceID, attemptID, source string, from *domain.InstanceState, to domain.InstanceState, details map[string]any, at time.Time) {
	s.eventSeq++
	ev := InstanceEvent{EventID: s.eventSeq, InstanceID: instanceID, AttemptID: attemptID, Source: source, FromState: from, ToState: to, Details: details, CreatedAt: at}
	s.instanceEventsByInst[instanceID] = append(s.instanceEventsByInst[instanceID], ev)
}

func (s *MemoryStore) refreshRunLocked(runID string, at time.Time) {
	r := s.runs[runID]
	if r == nil {
		return
	}
	counts := s.computeCountsLocked(runID)
	r.Counts = counts

	cancel := r.CancelRequested
	next := domain.NextRunState(r.State, counts, cancel)
	if next != r.State {
		from := r.State
		r.State = next
		s.emitRunEventLocked(runID, "orchestrator", &from, next, nil, at)
	}
	if r.State == domain.RunStateRunning && r.StartedAt == nil {
		t := at
		r.StartedAt = &t
	}
	if r.State.IsTerminal() && r.EndedAt == nil {
		t := at
		r.EndedAt = &t
	}
}

func (s *MemoryStore) computeCountsLocked(runID string) domain.RunCounts {
	counts := domain.RunCounts{}
	for _, id := range s.instanceIDsByRun[runID] {
		inst := s.instances[id]
		switch inst.State {
		case domain.InstanceStatePending:
			counts.Pending++
		case domain.InstanceStateSucceeded:
			counts.Succeeded++
		case domain.InstanceStateTestFailed:
			counts.TestFailed++
		case domain.InstanceStateInfraFailed:
			counts.InfraFailed++
		case domain.InstanceStateCanceled:
			counts.Canceled++
		default:
			counts.Running++
		}
	}
	return counts
}

func (s *MemoryStore) cloneRunLocked(in Run, includeBundle bool) Run {
	out := in
	if !includeBundle {
		out.Bundle = runbundle.Bundle{}
	}
	return out
}

func (s *MemoryStore) nextIDLocked(prefix string) string {
	s.idSeq++
	return fmt.Sprintf("%s_%d", prefix, s.idSeq)
}

func (s *MemoryStore) storeArtifactsLocked(runID, instanceID, attemptID string, artifacts []Artifact, at time.Time) {
	for idx, art := range artifacts {
		if strings.TrimSpace(art.ArtifactID) == "" {
			art.ArtifactID = defaultArtifactID(instanceID, attemptID, idx)
		}
		art.RunID = runID
		art.InstanceID = instanceID
		art.AttemptID = attemptID
		if art.CreatedAt.IsZero() {
			art.CreatedAt = at
		}
		s.artifactsByID[art.ArtifactID] = art
		s.artifactIDsByInst[instanceID] = append(s.artifactIDsByInst[instanceID], art.ArtifactID)
	}
}

func (s *MemoryStore) retryCountLocked(instanceID string) int {
	count := 0
	for _, ev := range s.instanceEventsByInst[instanceID] {
		if ev.Source == "retry" {
			count++
		}
	}
	return count
}

func defaultArtifactID(instanceID, attemptID string, idx int) string {
	attempt := strings.TrimSpace(attemptID)
	if attempt == "" {
		return fmt.Sprintf("art-%s-%03d", instanceID, idx+1)
	}
	return fmt.Sprintf("art-%s-%s-%03d", instanceID, attempt, idx+1)
}

func buildStoredResult(instanceID, attemptID, providerRef string, result InstanceResult, final domain.InstanceState, at time.Time) StoredInstanceResult {
	return StoredInstanceResult{
		InstanceID:      instanceID,
		AttemptID:       attemptID,
		FinalState:      final,
		ProviderRef:     providerRef,
		AgentRunID:      result.AgentRunID,
		AgentExitCode:   result.AgentExitCode,
		TrajectoryRef:   result.Trajectory,
		Usage:           usage.Clone(result.Usage),
		OracleExitCode:  result.OracleExitCode,
		OracleStdoutRef: result.OracleStdoutRef,
		OracleStderrRef: result.OracleStderrRef,
		TestExitCode:    result.TestExitCode,
		TestStdoutRef:   result.TestStdoutRef,
		TestStderrRef:   result.TestStderrRef,
		ErrorCode:       result.ErrorCode,
		ErrorMessage:    result.ErrorMessage,
		ErrorDetails:    result.ErrorDetails,
		ProvisionedAt:   result.ProvisionedAt,
		AgentStartedAt:  result.AgentStartedAt,
		AgentEndedAt:    result.AgentEndedAt,
		OracleStartedAt: result.OracleStartedAt,
		OracleEndedAt:   result.OracleEndedAt,
		TestStartedAt:   result.TestStartedAt,
		TestEndedAt:     result.TestEndedAt,
		CreatedAt:       at,
	}
}

func coalesce(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
