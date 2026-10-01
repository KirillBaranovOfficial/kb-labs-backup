package doctor

import "testing"

func TestDiagnoseJSONPreservesSecretAsRequiredInputOnly(t *testing.T) {
	response, err := DiagnoseJSON([]byte(`{"manifests":[{"ID":"plugin","Requirements":[{"ID":"plugin.token","Path":"/plugin/token","Secret":true,"Required":true,"Hint":"set token"}]}],"configured":{}}`))
	if err != nil || response.OK || len(response.Repair.SafeDefaults) != 0 || len(response.Repair.RequiredInput) != 1 {
		t.Fatalf("response/error = %#v / %v", response, err)
	}
}

func TestDiagnoseJSONKeepsOptionalCredentialWarningHealthy(t *testing.T) {
	data := []byte(`{"manifests":[{"id":"openai","requirements":[{"id":"openai.apiKey","path":"/platform/adapterOptions/llm/apiKey","secret":true,"optional":true,"required":true,"env":"OPENAI_API_KEY","action":"set OPENAI_API_KEY to enable LLM features"}]}]}`)
	response, err := DiagnoseJSON(data)
	if err != nil || !response.OK || len(response.Findings) != 1 || response.Findings[0].Severity != SeverityWarning || response.Findings[0].Action != "set OPENAI_API_KEY to enable LLM features" || len(response.Repair.RequiredInput) != 0 {
		t.Fatalf("optional credential diagnosis = %#v, %v", response, err)
	}
}
