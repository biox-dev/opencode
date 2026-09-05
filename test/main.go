package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	appcore "github.com/biox-dev/opencode/internal/app"
	"github.com/biox-dev/opencode/internal/config"
	"github.com/biox-dev/opencode/internal/db"
	"github.com/biox-dev/opencode/internal/message"
	"github.com/biox-dev/opencode/internal/pubsub"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	_, err = config.Load(wd, false)
	if err != nil {
		panic(err)
	}

	conn, err := db.Connect()
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	app, err := appcore.New(ctx, conn)
	if err != nil {
		panic(err)
	}
	defer app.Shutdown()

	sess, err := app.Sessions.Create(ctx, "headless-stream-demo")
	if err != nil {
		panic(err)
	}

	// In headless mode there is no interactive permission dialog. Auto-approve
	// avoids deadlocks when the model decides to call tools.
	app.Permissions.AutoApproveSession(sess.ID)

	msgCh := app.Messages.Subscribe(ctx)

	prompt := "请用3点总结这个仓库的定位"
	fmt.Printf("session=%s\n", sess.ID)
	fmt.Printf("prompt: %s\n\n", prompt)

	done, err := app.CoderAgent.Run(ctx, sess.ID, prompt)
	if err != nil {
		panic(err)
	}

	seenTextLen := map[string]int{}
	seenThinkingLen := map[string]int{}
	seenToolCalls := map[string]map[string]bool{}
	seenToolResults := map[string]map[string]bool{}

	for {
		select {
		case ev, ok := <-msgCh:
			if !ok {
				fmt.Println("\nmessage channel closed")
				return
			}
			handleMessageEvent(ev, sess.ID, seenTextLen, seenThinkingLen, seenToolCalls, seenToolResults)

		case result, ok := <-done:
			if !ok {
				return
			}
			if result.Error != nil {
				fmt.Printf("\nagent error: %v\n", result.Error)
				return
			}
			fmt.Printf("\n\n[FINAL]\n%s\n", result.Message.Content().String())
			return

		case <-ctx.Done():
			fmt.Printf("\ninterrupted: %v\n", ctx.Err())
			return
		}
	}
}

func handleMessageEvent(
	ev pubsub.Event[message.Message],
	sessionID string,
	seenTextLen map[string]int,
	seenThinkingLen map[string]int,
	seenToolCalls map[string]map[string]bool,
	seenToolResults map[string]map[string]bool,
) {
	msg := ev.Payload
	if msg.SessionID != sessionID {
		return
	}

	text := msg.Content().String()
	thinking := msg.ReasoningContent().Thinking

	if _, ok := seenToolCalls[msg.ID]; !ok {
		seenToolCalls[msg.ID] = map[string]bool{}
	}
	if _, ok := seenToolResults[msg.ID]; !ok {
		seenToolResults[msg.ID] = map[string]bool{}
	}

	switch ev.Type {
	case pubsub.CreatedEvent:
		seenTextLen[msg.ID] = 0
		seenThinkingLen[msg.ID] = 0
		fmt.Printf("[%s created] id=%s\n", msg.Role, msg.ID)
		if msg.Role == message.User && text != "" {
			fmt.Printf("[user] %s\n", text)
		}
		if msg.Role == message.Tool {
			for _, r := range msg.ToolResults() {
				fmt.Printf("[tool result %s] %s\n", r.ToolCallID, r.Content)
				seenToolResults[msg.ID][r.ToolCallID] = true
			}
		}

	case pubsub.UpdatedEvent:
		if msg.Role == message.Assistant {
			prevThinking := seenThinkingLen[msg.ID]
			if len(thinking) > prevThinking {
				fmt.Print("[thinking] ")
				fmt.Print(thinking[prevThinking:])
				fmt.Print("\n")
				seenThinkingLen[msg.ID] = len(thinking)
			}

			prev := seenTextLen[msg.ID]
			if len(text) > prev {
				fmt.Print(text[prev:])
				seenTextLen[msg.ID] = len(text)
			}
			if msg.IsFinished() {
				fmt.Printf("\n[assistant finished] reason=%s\n", msg.FinishReason())
			}
		}

		for _, c := range msg.ToolCalls() {
			if !seenToolCalls[msg.ID][c.ID] {
				fmt.Printf("\n[tool call start] id=%s name=%s\n", c.ID, c.Name)
				fmt.Printf("[tool call input] %s\n", c.Input)
				seenToolCalls[msg.ID][c.ID] = true
			}
			if c.Finished {
				fmt.Printf("[tool call finished] id=%s name=%s\n", c.ID, c.Name)
			}
		}

		if msg.Role == message.Tool {
			for _, r := range msg.ToolResults() {
				if !seenToolResults[msg.ID][r.ToolCallID] {
					fmt.Printf("[tool result %s] error=%v\n", r.ToolCallID, r.IsError)
					fmt.Printf("%s\n", r.Content)
					seenToolResults[msg.ID][r.ToolCallID] = true
				}
			}
		}
	}
}
