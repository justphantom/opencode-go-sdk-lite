package opencode

import "encoding/json"

// ============ 通用 ============

// LocationRef 定位一个工作区目录；至少给出 Directory。
// V1 接口以平铺 query（directory/workspace）传递，见 locationQuery。
type LocationRef struct {
	Directory   string `json:"directory"`
	WorkspaceID string `json:"workspaceID,omitempty"`
}

// ModelRef 引用一个 provider 模型；与 V1 Session.model 同构。
type ModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

// PromptModelRef 是 GET /agent 响应中 Agent.model 的模型引用（注意 wire 字段是 modelID）。
// prompt 请求侧统一用 ModelRef，由 SDK 内部转换（见 Client.Prompt）。
type PromptModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// toModelRef 把 wire 的 modelID 键名归一到 ModelRef.ID。
func (w PromptModelRef) toModelRef() *ModelRef {
	return &ModelRef{ID: w.ModelID, ProviderID: w.ProviderID}
}

// ============ Prompt ============

// PromptPart 是 prompt_async parts 的元素；当前仅覆盖 text，其余类型用 Raw 透传。
// ID 留空时由 SDK 生成（prt_ 前缀），见 Client.Prompt。
type PromptPart struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// PromptReq 对应 POST /session/{id}/prompt_async 的请求体。
// MessageID 留空时由 SDK 生成（msg_ 前缀），生成结果经 PromptAck 回传。
type PromptReq struct {
	MessageID string       `json:"-"`
	Model     *ModelRef    `json:"-"`
	Agent     string       `json:"agent,omitempty"`
	NoReply   bool         `json:"noReply,omitempty"`
	System    string       `json:"system,omitempty"`
	Variant   string       `json:"variant,omitempty"`
	Parts     []PromptPart `json:"parts"`
}

// PromptAck 是 Prompt 的回执：prompt_async 返 204 无 body，
// messageID/partID 是关联后续 SSE 事件（message.updated、message.part.*）的唯一句柄。
type PromptAck struct {
	MessageID string
	PartIDs   []string
}

// ============ Session ============

// SessionInfo 对应 V1 Session schema。
type SessionInfo struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug,omitempty"`
	ProjectID   string          `json:"projectID"`
	WorkspaceID string          `json:"workspaceID,omitempty"`
	Directory   string          `json:"directory"`
	Path        string          `json:"path,omitempty"`
	ParentID    string          `json:"parentID,omitempty"`
	Title       string          `json:"title"`
	Agent       string          `json:"agent,omitempty"`
	Model       *ModelRef       `json:"model,omitempty"`
	Version     string          `json:"version,omitempty"`
	Cost        float64         `json:"cost"`
	Tokens      SessionTokens   `json:"tokens"`
	Time        SessionTime     `json:"time"`
	Summary     *SessionSummary `json:"summary,omitempty"`
	Share       *SessionShare   `json:"share,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Permission  json.RawMessage `json:"permission,omitempty"`
	Revert      *RevertState    `json:"revert,omitempty"`
}

type SessionTokens struct {
	Input     float64      `json:"input"`
	Output    float64      `json:"output"`
	Reasoning float64      `json:"reasoning"`
	Cache     SessionCache `json:"cache"`
}

type SessionCache struct {
	Read  float64 `json:"read"`
	Write float64 `json:"write"`
}

// SessionTime 的时间戳为毫秒整数。
type SessionTime struct {
	Created    int64 `json:"created"`
	Updated    int64 `json:"updated"`
	Compacting int64 `json:"compacting,omitempty"`
	Archived   int64 `json:"archived,omitempty"`
}

type SessionSummary struct {
	Additions float64 `json:"additions"`
	Deletions float64 `json:"deletions"`
	Files     float64 `json:"files"`
}

type SessionShare struct {
	URL string `json:"url"`
}

// RevertState 对应 V1 Session.revert。
type RevertState struct {
	MessageID string `json:"messageID,omitempty"`
	PartID    string `json:"partID,omitempty"`
	Snapshot  string `json:"snapshot,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

// CreateSessionReq 对应 POST /session；Directory/WorkspaceID 走平铺 query，其余进 body。
type CreateSessionReq struct {
	ParentID    string         `json:"parentID,omitempty"`
	Title       string         `json:"title,omitempty"`
	Agent       string         `json:"agent,omitempty"`
	Model       *ModelRef      `json:"model,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Directory   string         `json:"-"`
	WorkspaceID string         `json:"workspaceID,omitempty"`
}

// ============ Messages ============

// SessionMessage 是 GET /session/{id}/message 的元素：消息元信息 + parts。
// Parts 保留原始 JSON（Part 有 10+ 种类型），调用方按需反序列化。
type SessionMessage struct {
	Info  MessageInfo       `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

// MessageInfo 是 User/Assistant 消息的公共字段（assistant 专有字段在 user 消息上为零值）。
// 更多字段（parts 之外的）请按 role 自行反序列化 Parts。
type MessageInfo struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionID"`
	Role      string        `json:"role"`
	Agent     string        `json:"agent,omitempty"`
	Finish    string        `json:"finish,omitempty"`
	Cost      float64       `json:"cost,omitempty"`
	Tokens    SessionTokens `json:"tokens,omitempty"`
}

// ============ Model / Provider ============

// ModelInfo 对应 V1 Model schema；Enabled 由 status=="active" 推导（见 ListModels）。
type ModelInfo struct {
	ID           string            `json:"id"`
	ProviderID   string            `json:"providerID"`
	Name         string            `json:"name"`
	Family       string            `json:"family,omitempty"`
	Status       string            `json:"status"`
	API          ModelAPI          `json:"api"`
	Capabilities ModelCapabilities `json:"capabilities"`
	Cost         ModelCost         `json:"cost"`
	Limit        ModelLimit        `json:"limit"`
	Options      map[string]any    `json:"options,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	ReleaseDate  string            `json:"release_date,omitempty"`
	Variants     json.RawMessage   `json:"variants,omitempty"`
	Enabled      bool              `json:"-"`
}

type ModelAPI struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	NPM string `json:"npm"`
}

// ModelCapabilities 按 V1 schema：tools 布尔 + input/output 数组。
type ModelCapabilities struct {
	Tools  bool     `json:"tools"`
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type ModelCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cache  struct {
		Read  float64 `json:"read"`
		Write float64 `json:"write"`
	} `json:"cache"`
}

type ModelLimit struct {
	Context int `json:"context"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output"`
}

// ProviderInfo 对应 V1 Provider schema；Models 以 modelID 为键。
type ProviderInfo struct {
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Source  string               `json:"source"`
	Env     []string             `json:"env,omitempty"`
	Options map[string]any       `json:"options,omitempty"`
	Models  map[string]ModelInfo `json:"models,omitempty"`
}

// providersResponse 是 GET /provider 的响应（listProviders 的中间结构）。
type providersResponse struct {
	All       []ProviderInfo    `json:"all"`
	Default   map[string]string `json:"default,omitempty"`
	Connected []string          `json:"connected,omitempty"`
}

// ============ Agent ============

// AgentInfo 对应 V1 Agent schema；permission/options 保留 RawMessage 透传。
// Model 已从 wire 的 modelID 键名归一到 ModelRef。
type AgentInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Mode        string          `json:"mode"` // subagent | primary | all
	Native      bool            `json:"native,omitempty"`
	Hidden      bool            `json:"hidden"`
	Color       string          `json:"color,omitempty"`
	Steps       int             `json:"steps,omitempty"`
	Model       *ModelRef       `json:"-"`
	Variant     string          `json:"variant,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Permission  json.RawMessage `json:"permission,omitempty"`
	Options     json.RawMessage `json:"options,omitempty"`
}

// UnmarshalJSON 把 wire 的 model.{providerID,modelID} 归一到 ModelRef。
func (a *AgentInfo) UnmarshalJSON(data []byte) error {
	type alias AgentInfo
	var raw struct {
		alias
		Model *PromptModelRef `json:"model"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = AgentInfo(raw.alias)
	if raw.Model != nil {
		a.Model = raw.Model.toModelRef()
	}
	return nil
}

// ============ Permission / Question ============

// PermissionRequest 对应 V1 PermissionRequest schema。
type PermissionRequest struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
	Always     []string        `json:"always,omitempty"`
	Tool       *PermissionTool `json:"tool,omitempty"`
}

type PermissionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// QuestionRequest 对应 V1 QuestionRequest schema。
type QuestionRequest struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Questions []QuestionInfo `json:"questions"`
	Tool      *QuestionTool  `json:"tool,omitempty"`
}

type QuestionInfo struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple,omitempty"`
	Custom   bool             `json:"custom,omitempty"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type QuestionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// QuestionReply.answers 与 questions 一一对应；每个元素是该问题的选中 label 列表。
type QuestionReply struct {
	Answers [][]string `json:"answers"`
}

// PermissionReply 取值：once / always / reject。
const (
	PermissionReplyOnce   = "once"
	PermissionReplyAlways = "always"
	PermissionReplyReject = "reject"
)

// ============ Event ============

// Event 是 SSE 推送的一条事件；Data 保留为原始 JSON，由调用方按 Type 反序列化。
type Event struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Event Type 常量。完整覆盖 spec 中 88 种事件字符串。
const (
	EventCatalogUpdated             = "catalog.updated"
	EventCommandExecuted            = "command.executed"
	EventFileEdited                 = "file.edited"
	EventFileWatcherUpdated         = "file.watcher.updated"
	EventGlobalDisposed             = "global.disposed"
	EventInstallationUpdateAvail    = "installation.update-available"
	EventInstallationUpdated        = "installation.updated"
	EventIntegrationConnUpdated     = "integration.connection.updated"
	EventIntegrationUpdated         = "integration.updated"
	EventLspUpdated                 = "lsp.updated"
	EventMcpBrowserOpenFailed       = "mcp.browser.open.failed"
	EventMcpToolsChanged            = "mcp.tools.changed"
	EventMessagePartDelta           = "message.part.delta"
	EventMessagePartRemoved         = "message.part.removed"
	EventMessagePartUpdated         = "message.part.updated"
	EventMessageRemoved             = "message.removed"
	EventMessageUpdated             = "message.updated"
	EventModelsDevRefreshed         = "models-dev.refreshed"
	EventPermissionAsked            = "permission.asked"
	EventPermissionReplied          = "permission.replied"
	EventPluginAdded                = "plugin.added"
	EventProjectDirectoriesUpdated  = "project.directories.updated"
	EventProjectUpdated             = "project.updated"
	EventPtyCreated                 = "pty.created"
	EventPtyDeleted                 = "pty.deleted"
	EventPtyExited                  = "pty.exited"
	EventPtyUpdated                 = "pty.updated"
	EventQuestionAsked              = "question.asked"
	EventQuestionRejected           = "question.rejected"
	EventQuestionReplied            = "question.replied"
	EventReferenceUpdated           = "reference.updated"
	EventServerConnected            = "server.connected"
	EventSessionCompacted           = "session.compacted"
	EventSessionCreated             = "session.created"
	EventSessionDeleted             = "session.deleted"
	EventSessionDiff                = "session.diff"
	EventSessionError               = "session.error"
	EventSessionIdle                = "session.idle"
	EventSessionNextAgentSwitched   = "session.next.agent.switched"
	EventSessionNextCompactionDelta = "session.next.compaction.delta"
	EventSessionNextCompactionEnded = "session.next.compaction.ended"
	EventSessionNextCompactionStart = "session.next.compaction.started"
	EventSessionNextContextUpdated  = "session.next.context.updated"
	EventSessionNextModelSwitched   = "session.next.model.switched"
	EventSessionNextMoved           = "session.next.moved"
	EventSessionNextPromptAdmitted  = "session.next.prompt.admitted"
	EventSessionNextPrompted        = "session.next.prompted"
	EventSessionNextReasoningDelta  = "session.next.reasoning.delta"
	EventSessionNextReasoningEnded  = "session.next.reasoning.ended"
	EventSessionNextReasoningStart  = "session.next.reasoning.started"
	EventSessionNextRetried         = "session.next.retried"
	EventSessionNextRevertCleared   = "session.next.revert.cleared"
	EventSessionNextRevertCommitted = "session.next.revert.committed"
	EventSessionNextRevertStaged    = "session.next.revert.staged"
	EventSessionNextShellEnded      = "session.next.shell.ended"
	EventSessionNextShellStarted    = "session.next.shell.started"
	EventSessionNextStepEnded       = "session.next.step.ended"
	EventSessionNextStepFailed      = "session.next.step.failed"
	EventSessionNextStepStarted     = "session.next.step.started"
	EventSessionNextSynthetic       = "session.next.synthetic"
	EventSessionNextTextDelta       = "session.next.text.delta"
	EventSessionNextTextEnded       = "session.next.text.ended"
	EventSessionNextTextStarted     = "session.next.text.started"
	EventSessionNextToolCalled      = "session.next.tool.called"
	EventSessionNextToolFailed      = "session.next.tool.failed"
	EventSessionNextToolInputDelta  = "session.next.tool.input.delta"
	EventSessionNextToolInputEnded  = "session.next.tool.input.ended"
	EventSessionNextToolInputStart  = "session.next.tool.input.started"
	EventSessionNextToolProgress    = "session.next.tool.progress"
	EventSessionNextToolSuccess     = "session.next.tool.success"
	EventSessionStatus              = "session.status"
	EventSessionUpdated             = "session.updated"
	EventTodoUpdated                = "todo.updated"
	EventTuiCommandExecute          = "tui.command.execute"
	EventTuiPromptAppend            = "tui.prompt.append"
	EventTuiSessionSelect           = "tui.session.select"
	EventTuiToastShow               = "tui.toast.show"
	EventVcsBranchUpdated           = "vcs.branch.updated"
	EventWorkspaceFailed            = "workspace.failed"
	EventWorkspaceReady             = "workspace.ready"
	EventWorkspaceStatus            = "workspace.status"
	EventWorktreeFailed             = "worktree.failed"
	EventWorktreeReady              = "worktree.ready"
)

// ============ scope 内高频事件的 Data struct ============

type TextDeltaData struct {
	Timestamp          float64 `json:"timestamp"`
	SessionID          string  `json:"sessionID"`
	AssistantMessageID string  `json:"assistantMessageID"`
	TextID             string  `json:"textID"`
	Delta              string  `json:"delta"`
}

type ToolCalledData struct {
	Timestamp          float64        `json:"timestamp"`
	SessionID          string         `json:"sessionID"`
	AssistantMessageID string         `json:"assistantMessageID"`
	CallID             string         `json:"callID"`
	Tool               string         `json:"tool"`
	Input              map[string]any `json:"input"`
}

type ToolSuccessData struct {
	Timestamp          float64           `json:"timestamp"`
	SessionID          string            `json:"sessionID"`
	AssistantMessageID string            `json:"assistantMessageID"`
	CallID             string            `json:"callID"`
	Structured         map[string]any    `json:"structured"`
	Content            []json.RawMessage `json:"content"`
}

type ToolFailedData struct {
	Timestamp          float64        `json:"timestamp"`
	SessionID          string         `json:"sessionID"`
	AssistantMessageID string         `json:"assistantMessageID"`
	CallID             string         `json:"callID"`
	Error              map[string]any `json:"error"`
}

type StepEndedData struct {
	Timestamp          float64    `json:"timestamp"`
	SessionID          string     `json:"sessionID"`
	AssistantMessageID string     `json:"assistantMessageID"`
	Finish             string     `json:"finish"`
	Cost               float64    `json:"cost"`
	Tokens             StepTokens `json:"tokens"`
}

type StepTokens struct {
	Input     float64   `json:"input"`
	Output    float64   `json:"output"`
	Reasoning float64   `json:"reasoning"`
	Cache     StepCache `json:"cache"`
}

type StepCache struct {
	Read  float64 `json:"read"`
	Write float64 `json:"write"`
}

// PermissionAskedData 是 permission.asked 的 data；与 PermissionRequest 同构。
type PermissionAskedData struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionID"`
	Permission string         `json:"permission"`
	Patterns   []string       `json:"patterns,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Always     []string       `json:"always,omitempty"`
}

// QuestionAskedData 是 question.asked 的 data；与 QuestionRequest 同构。
type QuestionAskedData struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Questions []QuestionInfo `json:"questions"`
	Tool      *QuestionTool  `json:"tool,omitempty"`
}

type SessionIdleData struct {
	SessionID string `json:"sessionID"`
}

type SessionErrorData struct {
	SessionID string         `json:"sessionID"`
	Error     map[string]any `json:"error"`
}
