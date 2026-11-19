package forge

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

// GitLab children come from the work-item hierarchy, labels included.
func TestChildrenFromGitLab(t *testing.T) {
	script(t, map[string]string{
		"glab repo view -F json": `{"path_with_namespace":"group/repo"}`,
		"glab api graphql -f query=" + childItemsQuery + " -f path=group/repo -f iid=71": `{"data":{"project":{
		"workItems":{"nodes":[{"widgets":[{},{"children":{"nodes":[
			{"iid":"72","title":"base","state":"OPEN","webUrl":"https://g/72","description":"",
			 "widgets":[{},{"labels":{"nodes":[{"title":"draft"}]}}]},
			{"iid":"73","title":"gone","state":"CLOSED","webUrl":"https://g/73","description":"","widgets":[]}
		]}}]}]}}}}`,
	})

	kids, err := Host{Name: "glab"}.Children(t.Context(), "71")
	if err != nil {
		t.Fatal(err)
	}

	if len(kids) != 2 || kids[0].Number != "72" || !slices.Equal(kids[0].Labels, []string{"draft"}) ||
		kids[0].URL != "https://g/72" || kids[1].State != "CLOSED" {
		t.Errorf("children = %+v", kids)
	}
}

// A GitLab without work items has no parent link to read.
func TestChildrenWithoutWorkItems(t *testing.T) {
	script(t, map[string]string{
		"glab repo view -F json": `{"path_with_namespace":"group/repo"}`,
		"glab api graphql -f query=" + childItemsQuery + " -f path=group/repo -f iid=71": "FAIL " +
			"Field 'workItems' doesn't exist on type 'Project'",
	})

	if _, err := (Host{Name: "glab"}).Children(t.Context(), "71"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// One issue shape reads both CLIs: object labels against plain ones, iid
// against number, and a merge request's url refused as no issue.
func TestIssueReadsBothCLIs(t *testing.T) {
	script(t, map[string]string{
		"gh issue view 7 --json " + issueFields: `{"number":7,"title":"T","state":"OPEN",` +
			`"url":"https://github.com/o/r/issues/7","labels":[{"name":"bug"}],"assignees":[{"login":"me"}]}`,
		"glab issue view 8 -F json": `{"iid":8,"title":"G","state":"opened",` +
			`"web_url":"https://gitlab.com/o/r/-/issues/8","labels":["bug"],"assignees":[{"username":"you"}]}`,
		"glab issue view 9 -F json": `{"iid":9,"web_url":"https://gitlab.com/o/r/-/merge_requests/9"}`,
	})

	gh, err := Host{Name: "gh"}.Issue(t.Context(), "7")
	if err != nil || gh.Number != "7" || gh.Labels[0] != "bug" || gh.Assignees[0] != "me" {
		t.Errorf("gh issue = %+v, %v", gh, err)
	}

	gl, err := Host{Name: "glab"}.Issue(t.Context(), "8")
	if err != nil || gl.Number != "8" || gl.Labels[0] != "bug" || gl.Assignees[0] != "you" || gl.URL == "" {
		t.Errorf("glab issue = %+v, %v", gl, err)
	}

	_, err = Host{Name: "glab"}.Issue(t.Context(), "9")
	if !errors.Is(err, ErrNotIssue) || err.Error() != "#9 is a pull request, not an issue" {
		t.Errorf("a merge request read as an issue: %v", err)
	}

	if item, err := (Host{Name: "glab"}).Item(t.Context(), "9"); err != nil || !item.Pull {
		t.Errorf("Item = %+v, %v; want the merge request flagged", item, err)
	}
}

// glab's JSON issue view carries no notes, so a read with comments asks the
// notes API, oldest first, leaves out system notes such as label changes, and
// flags internal notes.
func TestIssueWithCommentsReadsGitLabNotes(t *testing.T) {
	notes := "glab api --paginate projects/:fullpath/issues/%s/notes?per_page=100&sort=asc&order_by=created_at"

	script(t, map[string]string{
		"glab issue view 8 -F json": `{"iid":8,"title":"G","state":"opened","web_url":"https://gitlab.com/o/r/-/issues/8"}`,
		fmt.Sprintf(notes, "8"): `[{"id":1,"body":"squash the schema","system":false,"author":{"username":"ann"}},` +
			`{"id":2,"body":"added ~bug label","system":true,"author":{"username":"ann"}}]` + "\n" +
			`[{"id":3,"body":"agreed","system":false,"internal":true,"author":{"username":"bo"}}]`,
		"glab issue view 9 -F json": `{"iid":9,"title":"H","state":"opened","web_url":"https://gitlab.com/o/r/-/issues/9"}`,
		fmt.Sprintf(notes, "9"):     `[]`,
	})

	gl, err := Host{Name: "glab"}.IssueWithComments(t.Context(), "8")
	want := []IssueComment{{Author: "ann", Body: "squash the schema"}, {Author: "bo", Body: "agreed", Internal: true}}

	if err != nil || !slices.Equal(gl.Comments, want) {
		t.Errorf("glab comments = %+v, %v; want %+v", gl.Comments, err, want)
	}

	if bare, err := (Host{Name: "glab"}).IssueWithComments(t.Context(), "9"); err != nil || len(bare.Comments) != 0 {
		t.Errorf("glab issue without notes = %+v, %v", bare.Comments, err)
	}
}

// A create's number comes from the url the CLI prints last, in every issue
// url shape IssuePath accepts.
func TestCreateIssueReadsEveryIssueURL(t *testing.T) {
	for _, tc := range []struct{ host, out, url, number string }{
		{"gh", "https://github.com/o/r/issues/7\n", "https://github.com/o/r/issues/7", "7"},
		{"glab", "Creating issue\nhttps://gitlab.com/g/p/-/issues/8\n", "https://gitlab.com/g/p/-/issues/8", "8"},
		{"glab", "Creating issue\nhttps://gitlab.com/g/p/-/work_items/9\n", "https://gitlab.com/g/p/-/work_items/9", "9"},
	} {
		script(t, map[string]string{
			"gh issue create --title T --body B":                            tc.out,
			"glab issue create --title T --description B --no-editor --yes": tc.out,
		})

		url, number, err := Host{Name: tc.host}.CreateIssue(t.Context(), "T", "B", nil)
		if err != nil || url != tc.url || number != tc.number {
			t.Errorf("%s create of %q = %q, %q, %v; want %q, %q", tc.host, tc.out, url, number, err, tc.url, tc.number)
		}
	}
}
