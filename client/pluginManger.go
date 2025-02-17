package client

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenkins-zh/jenkins-cli/util"
)

// PluginManager is the client of plugin manager
type PluginManager struct {
	JenkinsCore

	UseMirror    bool
	MirrorURL    string
	ShowProgress bool
}

// Plugin represents a plugin of Jenkins
type Plugin struct {
	Active       bool
	Enabled      bool
	Bundled      bool
	Downgradable bool
	Deleted      bool
}

// InstalledPluginList represent a list of plugins
type InstalledPluginList struct {
	Plugins []InstalledPlugin
}

// AvailablePluginList represents a list of available plugins
type AvailablePluginList struct {
	Data   []AvailablePlugin
	Status string
}

// AvailablePlugin represetns a available plugin
type AvailablePlugin struct {
	Plugin

	// for the available list
	Name      string
	Installed bool
	Website   string
	Title     string
}

// InstalledPlugin represent the installed plugin from Jenkins
type InstalledPlugin struct {
	Plugin

	Enable             bool
	ShortName          string
	LongName           string
	Version            string
	URL                string
	HasUpdate          bool
	Pinned             bool
	RequiredCoreVesion string
	MinimumJavaVersion string
	SupportDynamicLoad string
	BackVersion        string
	Dependencies       []PluginDependency
}

var debugLogFile = "debug.html"

// CheckUpdate fetch the latest plugins from update center site
func (p *PluginManager) CheckUpdate(handle func(*http.Response)) (err error) {
	api := "/pluginManager/checkUpdatesServer"
	var response *http.Response
	response, err = p.RequestWithResponseHeader("POST", api, nil, nil, nil)
	if err == nil {
		p.handleCheck(handle)(response)
	}
	return
}

// GetAvailablePlugins get the aviable plugins from Jenkins
func (p *PluginManager) GetAvailablePlugins() (pluginList *AvailablePluginList, err error) {
	err = p.RequestWithData("GET", "/pluginManager/plugins", nil, nil, 200, &pluginList)
	return
}

// GetPlugins get installed plugins
func (p *PluginManager) GetPlugins(depth int) (pluginList *InstalledPluginList, err error) {
	if depth > 1 {
		err = p.RequestWithData("GET", fmt.Sprintf("/pluginManager/api/json?depth=%d", depth), nil, nil, 200, &pluginList)
	} else {
		err = p.RequestWithData("GET", "/pluginManager/api/json?depth=1", nil, nil, 200, &pluginList)
	}
	return
}

func (p *PluginManager) getPluginsInstallQuery(names []string) string {
	pluginNames := make([]string, 0)
	for _, name := range names {
		if name == "" {
			continue
		}
		if !strings.Contains(name, "@") {
			pluginNames = append(pluginNames, fmt.Sprintf("plugin.%s=", name))
		}
	}
	if len(pluginNames) == 0 {
		return ""
	}
	return strings.Join(pluginNames, "&")
}

func (p *PluginManager) getVersionalPlugins(names []string) []string {
	pluginNames := make([]string, 0)
	for _, name := range names {
		if strings.Contains(name, "@") {
			pluginNames = append(pluginNames, name)
		}
	}
	return pluginNames
}

// InstallPlugin install a plugin by name
func (p *PluginManager) InstallPlugin(names []string) (err error) {
	plugins := p.getPluginsInstallQuery(names)
	versionalPlugins := p.getVersionalPlugins(names)
	if plugins != "" {
		err = p.installPluginsWithoutVersion(plugins)
	}

	if err == nil && len(versionalPlugins) > 0 {
		err = p.installPluginsWithVersion(versionalPlugins)
	}
	return
}

func (p *PluginManager) installPluginsWithoutVersion(plugins string) (err error) {
	api := fmt.Sprintf("/pluginManager/install?%s", plugins)
	var response *http.Response
	response, err = p.RequestWithResponse("POST", api, nil, nil)
	if response != nil && response.StatusCode == 400 {
		if errMsg, ok := response.Header["X-Error"]; ok {
			for _, msg := range errMsg {
				err = fmt.Errorf(msg)
			}
		} else {
			err = fmt.Errorf("cannot found plugins %s", plugins)
		}
	}
	return
}

func (p *PluginManager) installPluginsWithVersion(plugins []string) (err error) {
	for _, plugin := range plugins {
		if err = p.installPluginWithVersion(plugin); err != nil {
			break
		}
	}
	return
}

// installPluginWithVersion install a plugin by name & version
func (p *PluginManager) installPluginWithVersion(name string) (err error) {
	pluginAPI := PluginAPI{
		RoundTripper: p.RoundTripper,
		UseMirror:    p.UseMirror,
		MirrorURL:    p.MirrorURL,
		ShowProgress: p.ShowProgress,
	}
	pluginName := "%s.hpi"
	pluginVersion := strings.Split(name, "@")

	defer os.Remove(fmt.Sprintf(pluginName, name))
	url := fmt.Sprintf("http://updates.jenkins-ci.org/download/plugins/%s/%s/%s.hpi",
		pluginVersion[0], pluginVersion[1], pluginVersion[0])

	url = pluginAPI.getMirrorURL(url)
	if err = pluginAPI.download(url, name); err == nil {
		err = p.Upload(fmt.Sprintf(pluginName, name))
	}
	return
}

// UninstallPlugin uninstall a plugin by name
func (p *PluginManager) UninstallPlugin(name string) (err error) {
	api := fmt.Sprintf("/pluginManager/plugin/%s/doUninstall", name)
	var (
		statusCode int
		data       []byte
	)

	if statusCode, data, err = p.Request("POST", api, nil, nil); err == nil {
		if statusCode != 200 {
			err = fmt.Errorf("unexpected status code: %d", statusCode)
			if p.Debug {
				ioutil.WriteFile(debugLogFile, data, 0664)
			}
		}
	}
	return
}

// Upload will upload a file from local filesystem into Jenkins
func (p *PluginManager) Upload(pluginFile string) (err error) {
	api := fmt.Sprintf("%s/pluginManager/uploadPlugin", p.URL)
	extraParams := map[string]string{}
	var request *http.Request
	if request, err = p.newfileUploadRequest(api, extraParams, "@name", pluginFile); err != nil {
		return
	}

	p.AuthHandle(request)

	client := p.GetClient()
	var response *http.Response
	if response, err = client.Do(request); err != nil {
		return
	} else if response.StatusCode != 200 {
		err = fmt.Errorf("StatusCode: %d", response.StatusCode)
		if data, readErr := ioutil.ReadAll(response.Body); readErr == nil && p.Debug {
			ioutil.WriteFile(debugLogFile, data, 0664)
		}
	}
	return err
}

func (p *PluginManager) handleCheck(handle func(*http.Response)) func(*http.Response) {
	if handle == nil {
		handle = func(*http.Response) {
			// Do nothing, just for avoid nil exception
		}
	}
	return handle
}

func (p *PluginManager) newfileUploadRequest(uri string, params map[string]string, paramName, path string) (req *http.Request, err error) {
	var file *os.File
	file, err = os.Open(path)
	if err != nil {
		return
	}

	var total float64
	var stat os.FileInfo
	if stat, err = file.Stat(); err != nil {
		return
	}
	total = float64(stat.Size())
	defer file.Close()

	bytesBuffer := &bytes.Buffer{}
	writer := multipart.NewWriter(bytesBuffer)

	var part io.Writer
	part, err = writer.CreateFormFile(paramName, filepath.Base(path))
	if err != nil {
		return
	}

	_, err = io.Copy(part, file)

	for key, val := range params {
		_ = writer.WriteField(key, val)
	}
	err = writer.Close()
	if err != nil {
		return
	}

	var progressWriter *util.ProgressIndicator
	if p.ShowProgress {
		progressWriter = &util.ProgressIndicator{
			Total:  total,
			Writer: bytesBuffer,
			Reader: bytesBuffer,
			Title:  "Uploading",
		}
		progressWriter.Init()
		req, err = http.NewRequest("POST", uri, progressWriter)
	} else {
		req, err = http.NewRequest("POST", uri, bytesBuffer)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	return
}
