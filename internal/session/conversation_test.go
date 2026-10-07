package session

import (
	"encoding/json"
	"testing"
)

func TestConversationIdentitySurvivesReloadAndChangesOnFresh(t *testing.T) {
	s := &Session{ID: "one"}
	first := s.ConversationID()
	s.Append(Message{Role: RoleUser, Text: "hello"})
	s.SetExternalID("claude", "external")
	if s.ConversationID() != first {
		t.Fatal("turn changed identity")
	}
	s.Fresh()
	second := s.ConversationID()
	if first == second {
		t.Fatal("fresh context kept identity")
	}
	b, _ := json.Marshal(s)
	var restored Session
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ConversationID() != second {
		t.Fatal("reload changed identity")
	}
	restored.Fresh() // even an empty scheduled run gets a new assignment
	if restored.ConversationID() == second {
		t.Fatal("empty fresh context kept identity")
	}
	restored.ForgetEngines()
	if restored.ConversationID() == second {
		t.Fatal("forgotten context kept identity")
	}
	other := &Session{ID: "two"}
	if other.ConversationID() == first {
		t.Fatal("new session reused identity")
	}
}
