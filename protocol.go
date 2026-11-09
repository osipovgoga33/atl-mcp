package main

import (
	"bufio"
	"encoding/json"
	"io"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var supportedProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const serverInstructions = `Confluence и Jira Server/DC.
Чтение — инструмент atl (action). Запись — atl_write, доступен только если включён ATL_ALLOW_WRITE.
Порядок правки страницы: atl conf_page с format=storage -> взять version -> atl_write conf_update с этим version и полным новым телом.
Тело страниц — Confluence storage format (XHTML с макросами ac:), не markdown.
Если страница изменилась после чтения, conf_update откажет: перечитать и слить правки.`

func serve(in io.Reader, out io.Writer, tools *toolset) {
	reader := bufio.NewReaderSize(in, 1<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if resp := handle(line, tools); resp != nil {
				_ = enc.Encode(resp)
			}
		}
		if err != nil {
			return
		}
	}
}

func handle(line []byte, tools *toolset) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		if len(trimSpaceBytes(line)) == 0 {
			return nil
		}
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	if len(req.ID) == 0 {
		return nil // уведомление: ответа нет
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedProtocols[0]
		for _, v := range supportedProtocols {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
			"instructions":    serverInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools.list()}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp
		}
		text, isErr := tools.call(p.Name, p.Arguments)
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\n' || b[j-1] == '\r' || b[j-1] == '\t') {
		j--
	}
	return b[i:j]
}
