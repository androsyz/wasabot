package agent

import (
	"reflect"
	"testing"
)

func TestDefaultDefinition(t *testing.T) {
	def, err := DefaultDefinition()
	if err != nil {
		t.Fatalf("the built-in agent must always parse: %v", err)
	}

	if def.Name == "" || def.Prompt == "" {
		t.Fatalf("def = %+v", def)
	}
	if !reflect.DeepEqual(def.Tools, []string{"current_time"}) {
		t.Fatalf("tools = %v", def.Tools)
	}
	if def.Model != "" || def.Language != "" {
		t.Fatalf("the built-in agent leaves the model to the configuration and the language to the customer: %+v", def)
	}
}
