package ghca

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// Collector is a worker to fetch repositories via GitHub Search API and clone them all.
// You should NOT reuse Collector instance for multiple queries.
type Collector struct {
	perPage uint
	maxPage uint
	page    uint
	// Query is a query to search repositories on GitHub.
	// Please refer following links to know about query:
	// https://help.github.com/articles/understanding-the-search-syntax/
	// https://help.github.com/articles/searching-repositories/
	Query string
	// Dest is a directory to clone repository into.
	Dest string
	// Extract is a regular expression to extract files with. It can be nil.
	Extract *regexp.Regexp
	// Count represents max number of repositories to clone
	Count int
	// Dry indicates doing dry-run instead of cloning repositories
	Dry bool
	// Deep indicates shallow clone is not used
	Deep bool
	// SSH indicates use of SSH protocol instead of HTTPS
	SSH    bool
	client *github.Client
	ctx    context.Context
}

func (col *Collector) searchRepos() (*github.RepositoriesSearchResult, error) {
	o := &github.SearchOptions{
		ListOptions: github.ListOptions{
			Page:    int(col.page),
			PerPage: int(col.perPage),
		},
	}
	r, _, err := col.client.Search.Repositories(col.ctx, col.Query, o)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Collect collects all repositories based on results of GitHub Search API. It returns total number
// of atucally cloned repositories and total number of repositories on GitHub.
func (col *Collector) Collect() (int, int, error) {
	log.Println("Searching GitHub repositories with query:", col.Query)
	start := time.Now()
	cloner := NewCloner(col.Dest, col.Extract, col.Deep, col.SSH)
	if !col.Dry {
		cloner.Start(col.Count)
	}

	total := 0
	count := 0
Fetch:
	for col.page <= col.maxPage {
		res, err := col.searchRepos()
		if _, ok := err.(*github.RateLimitError); ok {
			log.Println("Rate limit exceeded. Sleeping 1 minute")
			time.Sleep(1 * time.Minute)
			continue
		} else if err != nil {
			return 0, 0, err
		}

		total = res.GetTotal()

		if res.GetIncompleteResults() {
			log.Println("TODO: Handle incomplete result returned from GitHub API")
		}

		if len(res.Repositories) == 0 {
			// All repositories were searched
			break
		}

		for _, repo := range res.Repositories {
			slug := fmt.Sprintf("%s/%s", repo.GetOwner().GetLogin(), repo.GetName())
			if col.Dry {
				fmt.Printf("dry-run: %s: %s\n", slug, repo.GetDescription())
			} else {
				cloner.Clone(slug)
			}
			count++
			if col.Count > 0 && count >= col.Count {
				break Fetch
			}
		}

		col.page++
	}

	if !col.Dry {
		cloner.Shutdown()
		log.Printf("%d repositories were cloned into '%s' for total %d search results (%f seconds)\n", count, col.Dest, total, time.Now().Sub(start).Seconds())
	}

	return count, total, nil
}

// PageConfig represents configurations for pagination of the Search API.
type PageConfig struct {
	// Per represents how many repositories per sending request.
	Per uint
	// Max represents a max page.
	Max uint
	// Start represents which page should be started.
	Start uint
}

// PageUnlimited means to fetch and clone repositories as much as possible.
const PageUnlimited uint = 0

// NewCollector creates Collector instance.
func NewCollector(query, token, dest string, extract *regexp.Regexp, count int, dry bool, deep bool, ssh bool, page *PageConfig) *Collector {
	ctx := context.Background()

	var auth *http.Client
	if token != "" {
		src := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		auth = oauth2.NewClient(ctx, src)
	}

	client := github.NewClient(auth)
	c := &Collector{100, PageUnlimited, 1, query, dest, extract, count, dry, deep, ssh, client, ctx}

	if page != nil {
		c.perPage = page.Per
		c.maxPage = page.Max
		c.page = page.Start
	}
	if c.maxPage == PageUnlimited {
		maxRepos := 1000.0
		if 0 < count && count < 1000 {
			maxRepos = float64(count)
		}
		c.maxPage = uint(math.Ceil(maxRepos / float64(c.perPage)))
	}

	return c
}
