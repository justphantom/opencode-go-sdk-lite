package opencode

import (
	"encoding/json"
	"strings"
)

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

// PromptPart 是 prompt_async parts 的元素。
// text 类型填 Text；file 类型（附件）填 Mime/URL（Filename 可选）。
// ID 留空时由 SDK 生成（prt_ 前缀），见 Client.Prompt。
type PromptPart struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Filename string `json:"filename,omitempty"`
	URL      string `json:"url,omitempty"`
}

// PromptReq 对应 POST /session/{id}/prompt_async 的请求体。
// MessageID 留空时由 SDK 生成（msg_ 前缀），生成结果经 PromptAck 回传。
// Tools 是工具开关（工具名 → 是否启用），见 spec message body.tools。
type PromptReq struct {
	MessageID string          `json:"-"`
	Model     *ModelRef       `json:"-"`
	Agent     string          `json:"agent,omitempty"`
	NoReply   bool            `json:"noReply,omitempty"`
	System    string          `json:"system,omitempty"`
	Variant   string          `json:"variant,omitempty"`
	Tools     map[string]bool `json:"tools,omitempty"`
	Parts     []PromptPart    `json:"parts"`
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

// SessionStatus 是 GET /session/status 的单会话状态。
// 不在返回 map 中的会话视为 idle。
type SessionStatus struct {
	Type string `json:"type"` // idle | busy | retry
}

// SessionTokens 是 Session/Message 的 token 用量统计。
type SessionTokens struct {
	Input     float64      `json:"input"`
	Output    float64      `json:"output"`
	Reasoning float64      `json:"reasoning"`
	Cache     SessionCache `json:"cache"`
}

// SessionCache 是 token 用量的缓存命中部分。
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

// SessionSummary 是 session 的代码改动统计。
type SessionSummary struct {
	Additions float64 `json:"additions"`
	Deletions float64 `json:"deletions"`
	Files     float64 `json:"files"`
}

// SessionShare 是 session 的分享链接。
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

// PermissionRule 对应 V1 PermissionRule schema；Action 取值 allow / deny / ask。
type PermissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

// CreateSessionReq 对应 POST /session；Directory/WorkspaceID 走平铺 query，其余进 body。
type CreateSessionReq struct {
	ParentID    string           `json:"parentID,omitempty"`
	Title       string           `json:"title,omitempty"`
	Agent       string           `json:"agent,omitempty"`
	Model       *ModelRef        `json:"model,omitempty"`
	Metadata    map[string]any   `json:"metadata,omitempty"`
	Permission  []PermissionRule `json:"permission,omitempty"`
	Directory   string           `json:"-"`
	WorkspaceID string           `json:"workspaceID,omitempty"`
}

// ============ Messages ============

// SessionMessage 是 GET /session/{id}/message 的元素：消息元信息 + parts。
// Parts 保留原始 JSON（Part 有 10+ 种类型），调用方按需反序列化。
type SessionMessage struct {
	Info  MessageInfo       `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

// FinalText 组装消息的最终回复文本：拼接 type=="text" 且非
// synthetic/ignored 的 part.text；解析失败的 part 跳过。
// SSE 断连后用它从 ListMessages 历史重组最终回复（兜底）。
func (m SessionMessage) FinalText() string {
	var b strings.Builder
	for _, raw := range m.Parts {
		var p Part
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.Type != PartTypeText || p.Synthetic || p.Ignored {
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String()
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

// ModelAPI 是 provider 的 API 接入信息。
type ModelAPI struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	NPM string `json:"npm"`
}

// ModelCapabilities 按服务端实测结构（与 spec 声明不同）：
// input/output 是模态→布尔的对象，工具能力键为 toolcall。
type ModelCapabilities struct {
	Temperature bool            `json:"temperature,omitempty"`
	Reasoning   bool            `json:"reasoning,omitempty"`
	Attachment  bool            `json:"attachment,omitempty"`
	Toolcall    bool            `json:"toolcall"`
	Input       map[string]bool `json:"input,omitempty"`
	Output      map[string]bool `json:"output,omitempty"`
}

// ModelCost 是模型的单次计费（按 token 拆分，含缓存）。
type ModelCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cache  struct {
		Read  float64 `json:"read"`
		Write float64 `json:"write"`
	} `json:"cache"`
}

// ModelLimit 是模型的上下文/输入/输出 token 上限。
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

// PermissionTool 标记权限请求归属的工具调用。
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

// QuestionInfo 是单个问题的结构（question.asked 的元素）。
type QuestionInfo struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple,omitempty"`
	Custom   bool             `json:"custom,omitempty"`
}

// QuestionOption 是问题的候选项。
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionTool 标记问题请求归属的工具调用。
type QuestionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// QuestionReply 对应 question.reply 的 body；Answers 与 questions 一一对应，
// 每个元素是该问题的选中 label 列表。
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

// Event 是 SSE 推送的一条事件。实测 envelope 的数据字段是 properties
// （不是 spec 写的 data），保留为原始 JSON，由调用方按 Type 反序列化。
type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// Event Type 常量。覆盖服务端实测发出的事件（V1 经典事件体系，
// 实测不产生 session.next.* 与 *.v2.* 事件）。
const (
	EventCatalogUpdated            = "catalog.updated"
	EventCommandExecuted           = "command.executed"
	EventFileEdited                = "file.edited"
	EventFileWatcherUpdated        = "file.watcher.updated"
	EventGlobalDisposed            = "global.disposed"
	EventInstallationUpdateAvail   = "installation.update-available"
	EventInstallationUpdated       = "installation.updated"
	EventIntegrationConnUpdated    = "integration.connection.updated"
	EventIntegrationUpdated        = "integration.updated"
	EventLspUpdated                = "lsp.updated"
	EventMcpBrowserOpenFailed      = "mcp.browser.open.failed"
	EventMcpToolsChanged           = "mcp.tools.changed"
	EventMessagePartDelta          = "message.part.delta"
	EventMessagePartRemoved        = "message.part.removed"
	EventMessagePartUpdated        = "message.part.updated"
	EventMessageRemoved            = "message.removed"
	EventMessageUpdated            = "message.updated"
	EventModelsDevRefreshed        = "models-dev.refreshed"
	EventPermissionAsked           = "permission.asked"
	EventPermissionReplied         = "permission.replied"
	EventPluginAdded               = "plugin.added"
	EventProjectDirectoriesUpdated = "project.directories.updated"
	EventProjectUpdated            = "project.updated"
	EventPtyCreated                = "pty.created"
	EventPtyDeleted                = "pty.deleted"
	EventPtyExited                 = "pty.exited"
	EventPtyUpdated                = "pty.updated"
	EventQuestionAsked             = "question.asked"
	EventQuestionRejected          = "question.rejected"
	EventQuestionReplied           = "question.replied"
	EventReferenceUpdated          = "reference.updated"
	EventServerConnected           = "server.connected"
	EventSessionCompacted          = "session.compacted"
	EventSessionCreated            = "session.created"
	EventSessionDeleted            = "session.deleted"
	EventSessionDiff               = "session.diff"
	EventSessionError              = "session.error"
	EventSessionIdle               = "session.idle"
	EventSessionStatus             = "session.status"
	EventSessionUpdated            = "session.updated"
	EventTodoUpdated               = "todo.updated"
	EventTuiCommandExecute         = "tui.command.execute"
	EventTuiPromptAppend           = "tui.prompt.append"
	EventTuiSessionSelect          = "tui.session.select"
	EventTuiToastShow              = "tui.toast.show"
	EventVcsBranchUpdated          = "vcs.branch.updated"
	EventWorkspaceFailed           = "workspace.failed"
	EventWorkspaceReady            = "workspace.ready"
	EventWorkspaceStatus           = "workspace.status"
	EventWorktreeFailed            = "worktree.failed"
	EventWorktreeReady             = "worktree.ready"
)

// ============ scope 内高频事件的 properties struct（V1 实测格式） ============

// PartDeltaData 是 message.part.delta 的 properties。
// field 恒为 "text"；part 是 text 还是 reasoning 需结合 partID 查
// message.part.updated 中的 part.type（SDK 内部已做，见 mapToHighEvent）。
type PartDeltaData struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	PartID    string `json:"partID"`
	Field     string `json:"field"`
	Delta     string `json:"delta"`
}

// PartUpdatedData 是 message.part.updated 的 properties。
type PartUpdatedData struct {
	SessionID string `json:"sessionID"`
	Part      Part   `json:"part"`
	Time      int64  `json:"time"`
}

// Part.Type 取值常量。散落在 highevent.go/run.go/types.go 的判断里，
// 提常量避免拼写漂移导致 part 路由失败。
const (
	PartTypeText       = "text"
	PartTypeReasoning  = "reasoning"
	PartTypeTool       = "tool"
	PartTypeStepStart  = "step-start"
	PartTypeStepFinish = "step-finish"
)

// Part 是消息的一个组成块。type 取值：text / reasoning / tool /
// step-start / step-finish 等。tool 专有字段在 State。
type Part struct {
	ID        string     `json:"id"`
	MessageID string     `json:"messageID"`
	SessionID string     `json:"sessionID"`
	Type      string     `json:"type"`
	Text      string     `json:"text,omitempty"`
	Synthetic bool       `json:"synthetic,omitempty"` // text part：服务端合成，不计入最终回复
	Ignored   bool       `json:"ignored,omitempty"`   // text part：被忽略，不计入最终回复
	Reason    string     `json:"reason,omitempty"`    // step-finish 的终止原因，"stop" 为成功
	Tool      string     `json:"tool,omitempty"`
	CallID    string     `json:"callID,omitempty"`
	State     *ToolState `json:"state,omitempty"`
	Tokens    StepTokens `json:"tokens,omitempty"`
	Cost      float64    `json:"cost,omitempty"`
}

// ToolState 是 tool part 的执行状态。
type ToolState struct {
	Status string         `json:"status"` // pending | running | completed | error
	Input  map[string]any `json:"input,omitempty"`
	Output string         `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// MessageUpdatedData 是 message.updated 的 properties。
type MessageUpdatedData struct {
	SessionID string      `json:"sessionID"`
	Info      MessageInfo `json:"info"`
}

// StepTokens 是单个 step 的 token 用量（step.ended 携带）。
type StepTokens struct {
	Input     float64   `json:"input"`
	Output    float64   `json:"output"`
	Reasoning float64   `json:"reasoning"`
	Cache     StepCache `json:"cache"`
}

// StepCache 是 step 级 token 用量的缓存命中部分。
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

// SessionIdleData 是 session.idle 的 data；turn 结束兜底信号。
type SessionIdleData struct {
	SessionID string `json:"sessionID"`
}

// SessionErrorData 是 session.error 的 data；Error 字段至少含 message。
type SessionErrorData struct {
	SessionID string         `json:"sessionID"`
	Error     map[string]any `json:"error"`
}

// Todo 对应 V1 Todo schema。
type Todo struct {
	Content  string `json:"content"`
	Status   string `json:"status"` // pending | in_progress | completed | cancelled
	Priority string `json:"priority"`
}

// TodoUpdatedData 是 todo.updated 的 properties；Todos 为该会话当前完整列表。
type TodoUpdatedData struct {
	SessionID string `json:"sessionID"`
	Todos     []Todo `json:"todos"`
}
