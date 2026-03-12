package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	claudeAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	claudeModel      = "claude-sonnet-4-20250514"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	MaxTokens int     `json:"max_tokens"`
	Messages []Message `json:"messages"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ChatResponse struct {
	Content []ContentBlock `json:"content"`
}

type APIError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

var conversationHistory []Message

func help() {
	fmt.Println("help")
}

func handleCommand(input string) {
	command := strings.TrimPrefix(input, "/")
	command = strings.TrimSpace(command)

	switch command {
	case "help":
		help()
	default:
		fmt.Printf("Unknown command: /%s\n", command)
	}
}

func chat(userMessage string) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: ANTHROPIC_API_KEY environment variable is not set.")
		return
	}

	conversationHistory = append(conversationHistory, Message{
		Role:    "user",
		Content: userMessage,
	})

	reqBody := ChatRequest{
		Model:     claudeModel,
		MaxTokens: 1024,
		Messages:  conversationHistory,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Printf("Error marshalling request: %v\n", err)
		return
	}

	req, err := http.NewRequest("POST", claudeAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error sending request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr APIError
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
			fmt.Printf("API error (%d): %s\n", resp.StatusCode, apiErr.Error.Message)
		} else {
			fmt.Printf("API error (%d): %s\n", resp.StatusCode, string(body))
		}
		return
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	if len(chatResp.Content) > 0 && chatResp.Content[0].Type == "text" {
		reply := chatResp.Content[0].Text
		fmt.Println(reply)

		conversationHistory = append(conversationHistory, Message{
			Role:    "assistant",
			Content: reply,
		})
	} else {
		fmt.Println("No response from Claude.")
	}
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			handleCommand(input)
		} else {
			chat(input)
		}
	}
}
