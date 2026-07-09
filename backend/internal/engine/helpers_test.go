package engine

import (
	"context"
	"errors"
)

// This file holds test-only helpers shared across validate_test.go and
// compile_test.go (and any later tests in the engine package).

// fakeBoth implements both ActionNode and TriggerNode. Used to exercise the
// "must be exactly one" rule in Compile.
type fakeBoth struct{ fakeNode }

func (f *fakeBoth) Execute(*ExecutionContext, Payload) (Payload, error) { return nil, nil }
func (f *fakeBoth) Start(context.Context, Emitter) error                { return nil }
func (f *fakeBoth) Stop() error                                         { return nil }

func newTriggerFactory(typeName string) NodeFactory {
	return func() Node {
		return &fakeTrigger{fakeNode: fakeNode{typeName: typeName}}
	}
}

func newActionFactory(typeName string) NodeFactory {
	return func() Node {
		return &fakeAction{fakeNode: fakeNode{typeName: typeName}}
	}
}

func newFailingActionFactory(typeName string, initErr error) NodeFactory {
	return func() Node {
		return &fakeAction{fakeNode: fakeNode{typeName: typeName, initErr: initErr}}
	}
}

func newBothFactory(typeName string) NodeFactory {
	return func() Node {
		return &fakeBoth{fakeNode: fakeNode{typeName: typeName}}
	}
}

// newTestRegistry returns a Registry populated with the common test types:
//   - "trigger_a", "trigger_b" — TriggerNodes
//   - "action_a", "action_b" — ActionNodes
//   - "init_fails"           — ActionNode whose Init returns an error
//   - "plain"                — bare Node (neither Action nor Trigger)
//   - "both"                 — implements both ActionNode and TriggerNode
func newTestRegistry() *Registry {
	r := NewRegistry()
	r.Register("trigger_a", newTriggerFactory("trigger_a"))
	r.Register("trigger_b", newTriggerFactory("trigger_b"))
	r.Register("action_a", newActionFactory("action_a"))
	r.Register("action_b", newActionFactory("action_b"))
	r.Register("init_fails", newFailingActionFactory("init_fails", errors.New("boom")))
	r.Register("plain", newFakeFactory("plain"))
	r.Register("both", newBothFactory("both"))
	return r
}
