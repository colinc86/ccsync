---
name: community-query
description: Query patterns for non-academic CS sources — Hacker News (Algolia), Reddit, Stack Exchange. Reference for the research-community subagent. Covers endpoints, query syntax, curated subreddits, and User-Agent requirements.
user-invocable: false
---

# Community Query Reference

Three sources, all HTTP+JSON, no auth required for read access.

| Source | Best for |
|---|---|
| Hacker News (Algolia) | Tech news, launch announcements, practitioner debates |
| Reddit | Long-form practitioner discussion, subreddit-specific expertise |
| Stack Exchange | Concrete technical Q&A with vetted answers |

---

## Hacker News (via Algolia)

Base: `https://hn.algolia.com/api/v1`

Auth: none. Generous rate limits (~10k/hour shared).

### Search
```
GET /search?query=<topic>&tags=story&hitsPerPage=25
GET /search?query=<topic>&tags=(story,comment)&numericFilters=created_at_i>1735689600
GET /search_by_date?query=<topic>&tags=story  # most recent first
```

Tags:
- `story` — submissions
- `comment` — comments
- `poll`, `pollopt`, `show_hn`, `ask_hn`
- `author_<username>` — by author
- `(story,poll)` — OR within parens

Response shape:
```json
{
  "hits": [
    {
      "objectID": "12345678",
      "title": "...",
      "url": "https://...",
      "author": "pg",
      "points": 234,
      "num_comments": 89,
      "created_at_i": 1735689600,
      "story_text": "...",  // for Ask/Show HN
      "_highlightResult": {...}
    }
  ],
  "nbHits": 4567,
  "page": 0
}
```

### Companion URLs

For story `objectID=12345678`:
- HN page: `https://news.ycombinator.com/item?id=12345678`
- Algolia thread (with comments): `https://hn.algolia.com/api/v1/items/12345678` — full nested comment tree

### Useful filters
- `numericFilters=points>50` — only well-upvoted
- `numericFilters=num_comments>20` — only discussed
- `numericFilters=created_at_i>1735689600` — date floor (Unix timestamp)

---

## Reddit

Base: `https://www.reddit.com` (or `https://old.reddit.com`)

Auth: optional. Without auth: ~60 req/min; **MUST send a `User-Agent` header** like `User-Agent: claude-code-research/1.0 (by colinc86@gmail.com)`. Without UA you get blocked.

### Search across all subreddits
```
GET /search.json?q=<query>&sort=relevance&limit=25&t=year
```

`sort`: `relevance | hot | top | new | comments`
`t` (when sort=top): `hour | day | week | month | year | all`

### Search within a subreddit
```
GET /r/<subreddit>/search.json?q=<query>&restrict_sr=on&sort=top&t=year&limit=25
```

### Curated CS subreddits (by topic)

| Subreddit | Best for |
|---|---|
| r/MachineLearning | ML/AI papers, models, debates (academic-leaning) |
| r/LocalLLaMA | Open-source LLMs, quantization, inference |
| r/programming | General programming news |
| r/ExperiencedDevs | Senior-eng practitioner perspective |
| r/AskComputerScience | Educational Q&A |
| r/cscareerquestions | Industry signal, hiring, salaries |
| r/learnprogramming | Beginner pain points (useful for "what's confusing?") |
| r/rust | Rust-specific |
| r/cpp | C++-specific |
| r/golang | Go-specific |
| r/Python | Python-specific |
| r/javascript / r/node | JS/Node |
| r/devops | Infra, CI/CD, observability |
| r/kubernetes | K8s |
| r/databases | DB engineering |
| r/PostgreSQL | Postgres |
| r/sysadmin | Ops, real-world deployments |
| r/netsec | Security research |
| r/ReverseEngineering | RE, malware analysis |
| r/compsci | Theory, algorithms |
| r/algorithms | Algo discussion |
| r/embedded | Embedded systems |
| r/MachineLearningResearch | More technical than r/ML |

### Response shape
```json
{
  "data": {
    "children": [
      {
        "data": {
          "id": "abc123",
          "title": "...",
          "selftext": "...",
          "subreddit": "MachineLearning",
          "author": "...",
          "score": 234,
          "num_comments": 89,
          "created_utc": 1735689600,
          "permalink": "/r/MachineLearning/comments/abc123/title/",
          "url": "https://..."
        }
      }
    ]
  }
}
```

### Comments for a thread
```
GET /r/<subreddit>/comments/<id>.json?limit=50&depth=2
```

Returns array `[post, comments_tree]`. Walk `data.children[].data.body` for top-level + replies.

### Filtering anti-noise

- `score > 20` filters out un-engaged threads.
- `num_comments > 5` filters out solo-posts.
- Skip subreddits with `over18: true` if topic is general.

---

## Stack Exchange API

Base: `https://api.stackexchange.com/2.3`

Auth: API key optional (key gets 10k req/day; no key gets 300/day per IP). No key needed for casual use; if hitting limits, register an app.

Query parameter `site=` is **required**.

### Search questions
```
GET /search/advanced?order=desc&sort=votes&q=<topic>&site=stackoverflow&pagesize=25&filter=withbody
```

Filters control which fields come back. `withbody` includes the question body. Custom filter strings are pre-built — `withbody` is the safe default.

### Sites that matter for CS

| site= | Topic |
|---|---|
| stackoverflow | Programming Q&A (the big one) |
| serverfault | Sysadmin / infra |
| superuser | Power users |
| cs.stackexchange | CS theory and education |
| cstheory.stackexchange | Theoretical CS research |
| math.stackexchange | Math (often touches CS theory) |
| security.stackexchange | InfoSec |
| reverseengineering.stackexchange | RE |
| codereview.stackexchange | Code quality |
| softwareengineering.stackexchange | SE practices |
| ai.stackexchange | AI/ML Q&A |
| datascience.stackexchange | Data science |

For multi-site sweeps, run the same query across 3–5 relevant sites and merge by canonical question URL.

### Response shape
```json
{
  "items": [
    {
      "question_id": 12345,
      "title": "...",
      "body": "...",  // when filter=withbody
      "tags": ["python", "asyncio"],
      "score": 87,
      "answer_count": 4,
      "view_count": 12345,
      "is_answered": true,
      "accepted_answer_id": 67890,
      "creation_date": 1735689600,
      "owner": {"display_name": "..."},
      "link": "https://stackoverflow.com/questions/12345/..."
    }
  ],
  "has_more": true,
  "quota_remaining": 9876
}
```

### Get answers for a question
```
GET /questions/{ids}/answers?site=stackoverflow&filter=withbody&order=desc&sort=votes
```

### Useful filters
- `min=10` (with `sort=votes`) — only well-voted questions
- `tagged=python;asyncio` (semicolons OR; commas AND)
- `accepted=True` — only questions with accepted answers (signal for quality consensus)
- `fromdate=` / `todate=` — Unix timestamps

---

## jq snippets

```bash
# HN: extract well-engaged stories
jq '.hits[] | select(.points > 50) | {title, url, points, comments: .num_comments}'

# Reddit: top threads with score and url
jq '.data.children[].data | {title, score, comments: .num_comments, url, permalink: ("https://www.reddit.com" + .permalink)}'

# Stack Exchange: questions with accepted answers, sorted by score
jq '.items[] | select(.is_answered) | {title, score, link, answers: .answer_count, tags: (.tags | join(","))}'
```

---

## Output format expectation (research-community subagent writes these)

Per source-hit, write a `findings/<source>-<id>.md` with:
- Frontmatter: `id`, `source`, `url`, `title`, `score_or_points`, `engagement` (comment count), `created_at`, `tags` (if applicable), `relevance_self_score`
- Body: 2-4 sentence summary of what the thread/question is asserting + 1-3 quoted/paraphrased key claims with author attribution

These then feed into stage 5 critique alongside paper-derived findings.
