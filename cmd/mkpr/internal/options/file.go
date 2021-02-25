package options

import (
	"os"

	"github.com/sorfino/go-toolkit-cmd/internal/mkpr"
	"gopkg.in/yaml.v3"
)

func ParseFile(path string) (mkpr.BatchPullRequestOption, error) {
	var options mkpr.BatchPullRequestOption
	content, err := os.ReadFile(path)
	if err != nil {
		return options, err
	}

	err = yaml.Unmarshal(content, &options)
	return options, err
}
