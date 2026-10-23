package main

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

func TestJSONLines(t *testing.T) {
	events := []agent.Event{
		agent.EventAgentStart{},
		agent.EventTurnStart{Turn: 1},
		agent.EventAgentEnd{
			Usage: ai.Usage{
				Input:  10,
				Output: 5,
			},
		},
	}

	var b bytes.Buffer
	p := jsonLines{w: &b}
	for _, e := range events {
		p.print(e)
	}

	var got []agent.Event
	sc := bufio.NewScanner(&b)
	for sc.Scan() {
		e, err := agent.UnmarshalEvent(sc.Bytes())
		require.NoError(t, err)
		got = append(got, e)
	}
	require.NoError(t, sc.Err())
	assert.Equal(t, events, got)
}
