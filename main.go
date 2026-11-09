// atl-mcp — MCP-сервер для Confluence и Jira Server / Data Center (PAT).
// Только стандартная библиотека Go. Протокол — JSON-RPC по stdio.
package main

import (
	"log"
	"os"
	"strings"
)

const (
	serverName    = "atl-mcp"
	serverVersion = "0.4.2"
)

type config struct {
	ConfURL      string
	ConfToken    string
	JiraURL      string
	JiraToken    string
	AllowWrite   bool
	WriteSpaces  map[string]bool // пусто = любое пространство
	JiraWrite    map[string]bool // пусто = любой проект
	ConfInsecure bool
	JiraInsecure bool
	CAFile       string
	DownloadDir  string
}

func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "да"
}

func loadConfig() config {
	cfg := config{
		ConfURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("ATL_CONF_URL")), "/"),
		ConfToken:  strings.TrimSpace(os.Getenv("ATL_CONF_TOKEN")),
		JiraURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("ATL_JIRA_URL")), "/"),
		JiraToken:  strings.TrimSpace(os.Getenv("ATL_JIRA_TOKEN")),
		AllowWrite: envBool("ATL_ALLOW_WRITE"),
		// ATL_INSECURE_TLS — старый общий флаг, действует на оба
		ConfInsecure: envBool("ATL_CONF_INSECURE_TLS") || envBool("ATL_INSECURE_TLS"),
		JiraInsecure: envBool("ATL_JIRA_INSECURE_TLS") || envBool("ATL_INSECURE_TLS"),
		CAFile:       strings.TrimSpace(os.Getenv("ATL_CA_FILE")),
		WriteSpaces:  map[string]bool{},
		JiraWrite:    map[string]bool{},
		DownloadDir:  strings.TrimSpace(os.Getenv("ATL_DOWNLOAD_DIR")),
	}
	for _, s := range strings.Split(os.Getenv("ATL_JIRA_WRITE_PROJECTS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			cfg.JiraWrite[strings.ToUpper(s)] = true
		}
	}
	for _, s := range strings.Split(os.Getenv("ATL_WRITE_SPACES"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			cfg.WriteSpaces[strings.ToUpper(s)] = true
		}
	}
	return cfg
}

func main() {
	log.SetOutput(os.Stderr) // stdout принадлежит протоколу
	log.SetFlags(log.LstdFlags)
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		os.Stdout.WriteString(serverName + " " + serverVersion + "\n")
		return
	}
	cfg := loadConfig()
	ts := newToolset(cfg)
	spaces := "любые"
	if len(cfg.WriteSpaces) > 0 {
		keys := []string{}
		for k := range cfg.WriteSpaces {
			keys = append(keys, k)
		}
		spaces = strings.Join(keys, ",")
	}
	projects := "любые"
	if len(cfg.JiraWrite) > 0 {
		keys := []string{}
		for k := range cfg.JiraWrite {
			keys = append(keys, k)
		}
		projects = strings.Join(keys, ",")
	}
	log.Printf("старт %s %s: confluence=%q jira=%q запись=%v пространства записи=%s проекты записи=%s без проверки TLS: conf=%v jira=%v CA=%q",
		serverName, serverVersion, cfg.ConfURL, cfg.JiraURL, cfg.AllowWrite, spaces, projects, cfg.ConfInsecure, cfg.JiraInsecure, cfg.CAFile)
	serve(os.Stdin, os.Stdout, ts)
}
