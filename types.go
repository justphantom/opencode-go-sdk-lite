package opencode

import "encoding/json"

// ============ 通用 ============

// LocationRef 定位一个工作区目录；至少给出 Directory。
type LocationRef struct {
	Directory   string `json:"directory"`
	WorkspaceID string `json:"workspaceID,omitempty"`
}

// ModelRef 引用一个 provider 模型。
type ModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

// ============ Prompt ============

type PromptInput struct {
	Text   string                      `json:"text"`
	Files  []PromptInputFileAttachment `json:"files,omitempty"`
	Agents []PromptAgentAttachment     `json:"agents,omitempty"`
}

type PromptInputFileAttachment struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type PromptAgentAttachment struct {
	Source  string         `json:"source"`
	Agent   string         `json:"agent,omitempty"`
	Context map[string]any `json:"context,omitempty"`
}

// ============ Session ============

type SessionV2Info struct {
	ID        string        `json:"id"`
	ParentID  string        `json:"parentID,omitempty"`
	ProjectID string        `json:"projectID"`
	Agent     string        `json:"agent,omitempty"`
	Model     *ModelRef     `json:"model,omitempty"`
	Cost      float64       `json:"cost"`
	Tokens    SessionTokens `json:"tokens"`
	Time      SessionTime   `json:"time"`
	Title     string        `json:"title"`
	Location  *LocationRef  `json:"location"`
	Subpath   string        `json:"subpath,omitempty"`
	Revert    *RevertState  `json:"revert,omitempty"`
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

type SessionTime struct {
	Created  float64 `json:"created"`
	Updated  float64 `json:"updated"`
	Archived float64 `json:"archived,omitempty"`
}

type RevertState struct {
	Commit string `json:"commit,omitempty"`
	Staged bool   `json:"staged,omitempty"`
}

type SessionsResponse struct {
	Data   []SessionV2Info `json:"data"`
	Cursor *Cursor         `json:"cursor,omitempty"`
}

type Cursor struct {
	Previous string `json:"previous,omitempty"`
	Next     string `json:"next,omitempty"`
}

// CreateSessionReq 对应 POST /api/session 的请求体；至少留空由服务端生成 id。
type CreateSessionReq struct {
	ID       string       `json:"id,omitempty"`
	Agent    string       `json:"agent,omitempty"`
	Model    *ModelRef    `json:"model,omitempty"`
	Location *LocationRef `json:"location,omitempty"`
}

// PromptReq 对应 POST /api/session/{id}/prompt 的请求体。
type PromptReq struct {
	ID       string      `json:"id,omitempty"`
	Prompt   PromptInput `json:"prompt"`
	Delivery string      `json:"delivery,omitempty"`
	Resume   *bool       `json:"resume,omitempty"`
}

// SessionInputAdmitted 是 prompt 成功入队后的响应。
type SessionInputAdmitted struct {
	AdmittedSeq int64   `json:"admittedSeq"`
	ID          string  `json:"id"`
	SessionID   string  `json:"sessionID"`
	Prompt      Prompt  `json:"prompt"`
	Delivery    string  `json:"delivery"`
	TimeCreated float64 `json:"timeCreated"`
	PromotedSeq int64   `json:"promotedSeq,omitempty"`
}

type Prompt struct {
	Text   string            `json:"text"`
	Files  []json.RawMessage `json:"files,omitempty"`
	Agents []json.RawMessage `json:"agents,omitempty"`
}

// ============ Messages ============

// SessionMessage 是一条历史消息；spec 用 anyOf 覆盖 8 种变体，这里只做弱类型透传。
type SessionMessage struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func (m *SessionMessage) UnmarshalJSON(data []byte) error {
	m.Raw = append(m.Raw[:0], data...)
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	m.Type = head.Type
	return nil
}

type SessionMessagesResponse struct {
	Data   []SessionMessage `json:"data"`
	Cursor *Cursor          `json:"cursor,omitempty"`
}

// ============ Model / Provider ============

type ModelV2Info struct {
	ID           string            `json:"id"`
	ProviderID   string            `json:"providerID"`
	Family       string            `json:"family,omitempty"`
	Name         string            `json:"name"`
	API          ModelAPI          `json:"api"`
	Capabilities ModelCapabilities `json:"capabilities"`
	Request      json.RawMessage   `json:"request"`
	Variants     []json.RawMessage `json:"variants"`
	Time         ModelTime         `json:"time"`
	Cost         []json.RawMessage `json:"cost"`
	Status       string            `json:"status"`
	Enabled      bool              `json:"enabled"`
	Limit        ModelLimit        `json:"limit"`
}

type ModelAPI struct {
	Model string `json:"model"`
}

type ModelCapabilities struct {
	Reasoning   bool `json:"reasoning"`
	Tools       bool `json:"tools"`
	Vision      bool `json:"vision"`
	Audio       bool `json:"audio"`
	Output      bool `json:"output,omitempty"`
	SystemRole  bool `json:"systemRole,omitempty"`
	Attachments bool `json:"attachments,omitempty"`
	Batch       bool `json:"batch,omitempty"`
}

type ModelTime struct {
	Released float64 `json:"released"`
}

type ModelLimit struct {
	Context int `json:"context"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output"`
}

type ProviderV2Info struct {
	ID            string          `json:"id"`
	IntegrationID string          `json:"integrationID,omitempty"`
	Name          string          `json:"name"`
	Disabled      bool            `json:"disabled,omitempty"`
	API           json.RawMessage `json:"api"`
	Request       json.RawMessage `json:"request"`
}

// ============ Agent ============

// AgentV2Info 描述一个 agent；request 与 permissions 结构复杂，保留 RawMessage 透传。
type AgentV2Info struct {
	ID          string          `json:"id"`
	Model       *ModelRef       `json:"model,omitempty"`
	Request     json.RawMessage `json:"request"`
	System      string          `json:"system,omitempty"`
	Description string          `json:"description,omitempty"`
	Mode        string          `json:"mode"` // subagent | primary | all
	Hidden      bool            `json:"hidden"`
	Color       string          `json:"color,omitempty"`
	Steps       int             `json:"steps,omitempty"`
	Permissions json.RawMessage `json:"permissions"`
}

// ============ Permission / Question ============

type QuestionV2Request struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionID"`
	Questions []QuestionV2Info `json:"questions"`
	Tool      *QuestionV2Tool  `json:"tool,omitempty"`
}

type QuestionV2Info struct {
	Question string             `json:"question"`
	Header   string             `json:"header"`
	Options  []QuestionV2Option `json:"options"`
	Multiple bool               `json:"multiple,omitempty"`
	Custom   bool               `json:"custom,omitempty"`
}

type QuestionV2Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type QuestionV2Tool struct {
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

// ============ Permission / Question（补充） ============

type PermissionV2Request struct {
	ID        string              `json:"id"`
	SessionID string              `json:"sessionID"`
	Action    string              `json:"action"`
	Resources []string            `json:"resources"`
	Save      []string            `json:"save,omitempty"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
	Source    *PermissionV2Source `json:"source,omitempty"`
}

type PermissionV2Source struct {
	Type      string `json:"type"`
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// CreatePermissionReq 对应 POST /api/session/{id}/permission。
type CreatePermissionReq struct {
	ID        string              `json:"id,omitempty"`
	Action    string              `json:"action"`
	Resources []string            `json:"resources"`
	Save      []string            `json:"save,omitempty"`
	Metadata  map[string]any      `json:"metadata,omitempty"`
	Source    *PermissionV2Source `json:"source,omitempty"`
	Agent     string              `json:"agent,omitempty"`
}

type PermissionV2Created struct {
	ID     string `json:"id"`
	Effect string `json:"effect"` // allow | deny | ask
}

// ============ Event ============

// Event 是 SSE 推送的一条事件；Data 保留为原始 JSON，由调用方按 Type 反序列化。
type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Durable *Durable        `json:"durable,omitempty"`
}

// Durable 出现在 session-scoped 事件上，Seq 用于断线重连的 after 游标与去重。
type Durable struct {
	AggregateID string `json:"aggregateID"`
	Seq         int64  `json:"seq"`
	Version     int64  `json:"version"`
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
	EventPermissionV2Asked          = "permission.v2.asked"
	EventPermissionV2Replied        = "permission.v2.replied"
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
	EventQuestionV2Asked            = "question.v2.asked"
	EventQuestionV2Rejected         = "question.v2.rejected"
	EventQuestionV2Replied          = "question.v2.replied"
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

type PermissionAskedData struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Action    string         `json:"action"`
	Resources []string       `json:"resources"`
	Save      []string       `json:"save,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type QuestionAskedData struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionID"`
	Questions []QuestionV2Info `json:"questions"`
	Tool      *QuestionV2Tool  `json:"tool,omitempty"`
}

type SessionIdleData struct {
	SessionID string `json:"sessionID"`
}

type SessionErrorData struct {
	SessionID string         `json:"sessionID"`
	Error     map[string]any `json:"error"`
}
