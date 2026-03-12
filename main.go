package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	claudeAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	claudeModel      = "claude-sonnet-4-20250514"
)

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ChatRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type ChatResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
}

type APIError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type ToolResultContent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type ShellInput struct {
	Command string `json:"command"`
}

type ReadSkillInput struct {
	Filename string `json:"filename"`
}

var conversationHistory []Message

func getSystemPrompt() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	installCmd := "apt-get install -y"
	if osName == "darwin" {
		installCmd = "brew install"
	}

	return fmt.Sprintf(`You are a helpful assistant running in a %s/%s environment. You have full access to the system's shell and all installed command-line tools.

If a tool or command is not installed, you can install it using: %s <package-name>

Use the "shell" tool to execute any commands on the system. You can run any valid shell command. Always use the shell tool when the user asks you to perform system operations, file manipulations, installations, or any task that requires running commands.

When executing commands:
- Prefer concise, single-purpose commands
- Chain commands with && when appropriate
- Always consider the current OS (%s) when choosing commands

You also have a list of tools or skills available in a folder called ".skills" In it you will find extra tools and skills you need to fulfill a task. Each skill is in a separate file.

To discover and use skills:
1. Use the "list_skills" tool to see all available skill files in the .skills folder
2. Use the "read_skill" tool to read the content of a specific skill file
3. The skill file will describe additional tools, functions, or instructions you can use — follow those instructions to complete the task

Before starting a complex task, always check the .skills folder for any relevant skills that can help you.`, osName, arch, installCmd, osName)
}

func getTools() []Tool {
	return []Tool{
		{
			Name:        "shell",
			Description: "Execute a shell command on the system. Returns the stdout and stderr output of the command.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {
						"type": "string",
						"description": "The shell command to execute"
					}
				},
				"required": ["command"]
			}`),
		},
		{
			Name:        "list_skills",
			Description: "List all available skill files in the .skills folder. Returns the filenames of all skills available for use.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {},
				"required": []
			}`),
		},
		{
			Name:        "read_skill",
			Description: "Read the content of a specific skill file from the .skills folder. The skill file contains instructions, tool definitions, or other information needed to fulfill a task.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"filename": {
						"type": "string",
						"description": "The name of the skill file to read (e.g. 'docker.md')"
					}
				},
				"required": ["filename"]
			}`),
		},
	}
}

func truncateToWords(s string, maxWords int) string {
	words := strings.Fields(s)
	if len(words) <= maxWords {
		return s
	}
	return strings.Join(words[:maxWords], " ") + fmt.Sprintf("\n... [truncated, showing %d of %d words]", maxWords, len(words))
}

func printToolResult(result string) {
	preview := truncateToWords(result, 200)
	fmt.Printf("📎 Result: %s\n", preview)
}

func executeShell(command string) string {
	fmt.Printf("🔧 Executing: %s\n", command)

	cmd := exec.Command("bash", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var result strings.Builder
	if stdout.Len() > 0 {
		result.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("STDERR: " + stderr.String())
	}
	if err != nil {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("Exit error: %v", err))
	}
	if result.Len() == 0 {
		result.WriteString("Command completed successfully (no output).")
	}

	printToolResult(result.String())
	return result.String()
}

func listSkills() string {
	fmt.Println("📂 Listing skills...")

	entries, err := os.ReadDir(".skills")
	if err != nil {
		if os.IsNotExist(err) {
			return "No .skills folder found. Create a .skills directory and add skill files to it."
		}
		return fmt.Sprintf("Error reading .skills folder: %v", err)
	}

	if len(entries) == 0 {
		return "The .skills folder is empty. No skills available."
	}

	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			skills = append(skills, entry.Name())
		}
	}

	if len(skills) == 0 {
		return "The .skills folder contains no skill files."
	}

	result := fmt.Sprintf("Available skills (%d):\n", len(skills))
	for _, s := range skills {
		result += fmt.Sprintf("  - %s\n", s)
	}

	printToolResult(result)
	return result
}

func readSkill(filename string) string {
	fmt.Printf("📖 Reading skill: %s\n", filename)

	// Sanitize: prevent path traversal
	clean := filepath.Clean(filename)
	if strings.Contains(clean, "..") || filepath.IsAbs(clean) {
		return "Error: invalid filename. Use only the skill filename, not a path."
	}

	path := filepath.Join(".skills", clean)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Skill file '%s' not found. Use list_skills to see available skills.", filename)
		}
		return fmt.Sprintf("Error reading skill file: %v", err)
	}

	content := string(data)
	printToolResult(content)
	return content
}

func executeTool(name string, input json.RawMessage) string {
	switch name {
	case "shell":
		var shellInput ShellInput
		if err := json.Unmarshal(input, &shellInput); err != nil {
			return fmt.Sprintf("Error parsing tool input: %v", err)
		}
		return executeShell(shellInput.Command)
	case "list_skills":
		return listSkills()
	case "read_skill":
		var skillInput ReadSkillInput
		if err := json.Unmarshal(input, &skillInput); err != nil {
			return fmt.Sprintf("Error parsing tool input: %v", err)
		}
		return readSkill(skillInput.Filename)
	default:
		return fmt.Sprintf("Unknown tool: %s", name)
	}
}

func sendRequest(apiKey string) (*ChatResponse, error) {
	reqBody := ChatRequest{
		Model:     claudeModel,
		MaxTokens: 4096,
		System:    getSystemPrompt(),
		Messages:  conversationHistory,
		Tools:     getTools(),
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request: %v", err)
	}

	req, err := http.NewRequest("POST", claudeAPIURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr APIError
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("error parsing response: %v", err)
	}

	return &chatResp, nil
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

	for {
		resp, err := sendRequest(apiKey)
		if err != nil {
			fmt.Println(err)
			return
		}

		// Print any text blocks and collect tool uses
		var toolResults []ToolResultContent
		var assistantContent []ContentBlock

		for _, block := range resp.Content {
			assistantContent = append(assistantContent, block)

			switch block.Type {
			case "text":
				if block.Text != "" {
					fmt.Println(block.Text)
				}
			case "tool_use":
				result := executeTool(block.Name, block.Input)
				toolResults = append(toolResults, ToolResultContent{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   result,
				})
			}
		}

		// Store the assistant's full response in history
		conversationHistory = append(conversationHistory, Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		// If there were tool calls, send results back and continue the loop
		if len(toolResults) > 0 {
			conversationHistory = append(conversationHistory, Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		// No tool calls — we're done
		break
	}
}

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

func showStartup() {
	logo := `
  _          _ _                            _     _ 
 | |__   ___| | | ___   __      _____  _ __| | __| |
 | '_ \ / _ \ | |/ _ \  \ \ /\ / / _ \| '__| |/ _' |
 | | | |  __/ | | (_) |  \ V  V / (_) | |  | | (_| |
 |_| |_|\___|_|_|\___/    \_/\_/ \___/|_|  |_|\__,_|
`
	fmt.Print(logo)

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	msg := "Initializing"

	for i := 0; i < 20; i++ {
		frame := frames[i%len(frames)]
		fmt.Printf("\r  %s %s...", frame, msg)
		time.Sleep(80 * time.Millisecond)
	}
	fmt.Print("\r  ✔ Ready!         \n\n")
}

func main() {
	showStartup()

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
