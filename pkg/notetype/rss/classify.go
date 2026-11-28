package rss

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i5heu/MentisEterna/internal/llm"
	"github.com/i5heu/MentisEterna/pkg/notetype/llminferencer"
)

// classifyMaxRunes caps the article text sent to the classifier.
const classifyMaxRunes = 10_000

// classifyTimeBudget bounds how long one refresh's classify stage spends
// draining the pending queue (multiple batches); the refresh task's own
// deadline is 10 min, so this leaves headroom.
const classifyTimeBudget = 8 * time.Minute

// Classification is the structured result the evaluation prompt produces:
// a significance score plus the dimensions it is computed from (human
// impact, negativity, personal relevance). The contract follows the
// significance-evaluation prompt (defaultPrompt); the legacy category /
// summary / quality fields are still parsed defensively so custom prompts
// (or pre-evaluation data) degrade gracefully instead of failing.
type Classification struct {
	EvaluationReflectionParagraph                     string   `json:"evaluationReflectionParagraph"`
	Significance                                      float64  `json:"significance"`
	Negativity                                        float64  `json:"negativity"`
	PersonalRelevance                                 float64  `json:"personalRelevance"`
	AlreadyDirectlyAffectedHumansInMillions           float64  `json:"alreadyDirectlyAffectedHumansInMillions"`
	AlreadyIndirectlyAffectedHumansInMillions         float64  `json:"alreadyIndirectlyAffectedHumansInMillions"`
	PotentialFutureDirectlyAffectedHumansInMillions   float64  `json:"potentialFutureDirectlyAffectedHumansInMillions"`
	PotentialFutureIndirectlyAffectedHumansInMillions float64  `json:"potentialFutureIndirectlyAffectedHumansInMillions"`
	Tags                                              []string `json:"tags"`
	// Error carries the prompt's optional "error" diagnostics (fields that
	// could not be identified): stored to classify_error, still status done.
	Error string `json:"error"`

	// Legacy fields (pre-evaluation prompts): parsed when present, empty
	// with the default prompt, and NEVER persisted by the pipeline — the
	// telegraph digest (summary_json) is the one and only summary.
	Category string `json:"category"`
	Summary  string `json:"summary"`
	Quality  int    `json:"quality"`
}

// metrics mirrors the evaluation into the score-formula input shape.
func (c Classification) metrics() evalMetrics {
	return evalMetrics{
		Significance:   c.Significance,
		Negativity:     c.Negativity,
		Relevance:      c.PersonalRelevance,
		NowDirect:      c.AlreadyDirectlyAffectedHumansInMillions,
		NowIndirect:    c.AlreadyIndirectlyAffectedHumansInMillions,
		FutureDirect:   c.PotentialFutureDirectlyAffectedHumansInMillions,
		FutureIndirect: c.PotentialFutureIndirectlyAffectedHumansInMillions,
	}
}

// hasEvaluation reports whether the reply carried real evaluation data (as
// opposed to a legacy category-only reply): the reflection paragraph is the
// strongest marker, the score dimensions add confidence. Items without an
// evaluation never fall below the min_points floor.
func (c Classification) hasEvaluation() bool {
	return c.EvaluationReflectionParagraph != "" || c.Significance > 0 ||
		c.Negativity > 0 || c.PersonalRelevance > 0 ||
		c.AlreadyDirectlyAffectedHumansInMillions > 0 || c.AlreadyIndirectlyAffectedHumansInMillions > 0 ||
		c.PotentialFutureDirectlyAffectedHumansInMillions > 0 || c.PotentialFutureIndirectlyAffectedHumansInMillions > 0
}

// defaultPrompt is the built-in significance-evaluation prompt, used when no
// rss_prompt note is in scope (or an in-scope prompt note is empty). It
// assesses an article's global impact — significance 0.0–1.0 on a defined
// scale, negativity, personal relevance (Germany/EU + cloud/distributed-
// systems context), and humans-affected estimates — and reflects on the
// assessment in a paragraph. Override per reader by placing an rss_prompt
// note in (or above) the reader.
const defaultPrompt = `You are a neutral system dedicated to objectively observing and evaluating event significance without personal involvement or bias. Your primary responsibility is to assess and quantify the global impact of events, emphasizing their cumulative effect on humanity rather than isolated consequences.

Begin with a concise checklist (3-7 bullets) outlining each major step you will take before starting your analysis. For any field that cannot be identified due to incomplete or ambiguous article input, use the default values (0.0, empty string, or array as indicated) and briefly describe the issue in the "error" field.

Task: Evaluate the significance of a provided article by assessing its impact on global society, economy, politics, and long-term trends, using a scale from 0.0 to 1.0 according to the provided significance scale definitions and examples.

# Instructions

- Assess the impact of the event on humanity, prioritizing positive developments and scientific advancements.
- Adhere to the significance scale definitions and examples for accurate classification.

# Steps

1. Estimate Event Scope: Evaluate the number of individuals affected by the event, directly or indirectly, both now and in the future. Complete these fields:
   - Potential Future Directly Affected Humans (millions): Estimate of individuals potentially directly impacted (float).
   - Potential Future Indirectly Affected Humans (millions): Estimated indirect future impact (float).
   - Already Directly Affected Humans (millions): Individuals already experiencing direct effects (float).
   - Already Indirectly Affected Humans (millions): Individuals already indirectly impacted (float).
2. Comprehend the Article: Extract key facts, statements, and broader global implications by thoroughly reviewing its content.
3. Assess Broader Impact: Judge effects on global society, economic systems, political environments, policy, and enduring trends.
4. Cross-Reference Examples: Match article content to the sample categories in the significance scale to determine closest impact level.
5. Select Significance Level: Reflect—in at least one sentence—on the article's placement on the scale, then select a score (0.0 to 1.0).
6. Apply Critical Evaluation: Consider reasons for lowering the score; default to a conservative estimate.
7. Classify Event Negativity: Assign 0.0 (positive), 1.0 (negative), or 0.5 (neutral) based on net benefit or harm to humanity.
8. Score Personal Relevance: Using the personal context below, assign a personalRelevance score between 0.0 and 1.0 (higher = more relevant to Mia in Germany/EU).
9. Finalize Significance: Consolidate all insights to determine the final significance level.
10. Assign Tags: Generate up to 10 relevant tags to enhance article searchability and categorization.

# Significance Scale Definitions

0.0 - No Impact

- Definition: Events or statements with no influence on society, economy, politics, or policies. They do not affect any stakeholders.
- Examples:
  1. A local weather report indicating typical conditions.
  2. A personal blog post about someone's daily routine with no broader relevance.
  3. A routine maintenance update for a small website.

0.1 - Minimal Impact

- Definition: Local events of limited interest with negligible effects on society or policies. They may be noted but do not prompt action or discussion.
- Examples:
  1. A small community art exhibit opening.
  2. A minor change in a company's branding that goes largely unnoticed.
  3. A local sports team winning a game.

0.2 - Low Impact

- Definition: Local events that are noteworthy within a small community but have little to no impact beyond that.
- Examples:
  1. A local election for a city council position.
  2. Opening of a new local business.
  3. A community protest with a small turnout.
  4. New Deaths in an ongoing war.
  5. In an war, a new event that took less then 10000 lives.

0.3 - Moderate-Low Impact

- Definition: Events causing limited discussions among specific groups but do not lead to significant changes. Ongoing situations without new developments. Minimum of 10000 humans affected.
- Examples:
  1. A regional conference on emerging technologies attended by industry professionals.
  2. An ongoing war without new significant escalation.
  3. A scientific study published in a niche journal.

0.5 - Noticeable Impact

- Definition: Events attracting national attention, influencing public discourse, and possibly leading to policy considerations. Depressing news with high casualty rates should be assigned a lower significance level within this range.
- Examples:
  1. Introduction of a bill proposing changes to existing laws with initial debates.
  2. National discussions on social issues prompted by recent events.
  3. Reports of increased antisemitism affecting community safety.

0.7 - High Impact

- Definition: Events causing significant shifts in societal attitudes, major scientific advancements, or policy changes impacting large populations. Positive news and advancements are emphasized. Only if it has direct implications on all humans.
- Examples:
  1. A major technological breakthrough altering industry practices globally.
  2. A Supreme Court ruling that changes national laws.
  3. A global agreement on climate change policies affecting economies.

0.8 - Very High Impact

- Definition: Events leading to transformative changes in global society or the economy, with long-lasting positive effects. Scientific advancements and positive news are prioritized. Only if it has direct implications on all humans.
- Examples:
  1. A groundbreaking medical discovery curing a major disease.
  2. Global initiatives leading to significant environmental restoration.
  3. Technological innovations revolutionizing daily life worldwide.

1.0 - Maximum Impact

- Definition: Events with profound and far-reaching consequences, reshaping global society, economy, or politics, especially those affecting humanity as a whole and long-term trends. Only if it has direct implications on all humans.
- Examples:
  1. The outbreak of a global pandemic causing worldwide disruption.
  2. Declaration of a major war affecting multiple nations with significant escalation.
  3. A country amending its constitution to recognize new fundamental human rights.
  4. Nuclear war

# Personal Relevance

Purpose: In addition to global significance, estimate how personally relevant this event is to the user, using the context below.

## Personal context (use for relevance scoring)

* Location & timezone: Germany (Europe/Berlin); EU context matters most.
* Work & interests: Cloud Engineering, Go (Golang), distributed systems, databases, DevOps/CI, LLM infrastructure and moderation tooling.
* Strong relevance boosters:

  * Medical/health breakthroughs, especially with practical human impact.
  * Technical advances in software/cloud/distributed systems/AI tooling.
  * EU/Germany laws, regulations, safety, healthcare, or major economic changes affecting Germany/EU.
* Relevance reducers:

  * US-only domestic politics/culture issues are usually low relevance **unless** there is a clear mechanism that directly affects the user in Germany/EU (e.g., global tech/platform policy changes, sanctions affecting EU markets, major security incidents impacting widely used services, etc.).
* Personal wellbeing: ADHD-related life management topics can be relevant if the article is about actionable health/therapy/clinical advances or major policy changes in healthcare access.

## Personal relevance scale

* 0.0 = Not personally relevant (no plausible effect on the user).
* 0.3 = Mild relevance (interesting, but unlikely to change the user's life/work).
* 0.6 = High relevance (likely impacts the user's work, health, or EU/Germany context).
* 0.8–1.0 = Very high to extreme relevance (strong, near-term impact on the user's health, safety, or core technical domain; or major EU/Germany policy changes directly affecting her).

## Personal relevance rules

* Be conservative; if unclear, score lower.
* If the article is US-centric and lacks a concrete impact pathway to Germany/EU, keep ≤ 0.3.
* Medical/health breakthroughs with strong evidence and plausible adoption: bias upward.
* Major technical breakthroughs in cloud/AI/distributed systems with credible adoption pathway: bias upward.

# Human Impact Counting Rules (Millions)

Purpose: Fill the four "humans affected (millions)" fields consistently, using the same meaning across articles.

# A) Directly affected (D)
People who experience a *primary, first-order* change due to the event, without needing additional intermediaries.
They are part of the event's immediate target group or exposure group, and the effect is tangible/measurable at the person-level.
Typical signals: legal status changes; access/denial of a service; medical outcomes; job loss/gain; displacement; direct financial transfers/costs; direct safety/health exposure; directly regulated or sanctioned population.

# B) Indirectly affected (I)
People who experience a *secondary, downstream* change because direct effects propagate through systems (economy, supply chains, institutions, information, environment).
They are not the primary exposure/target group, but may see material changes in prices, availability, risk, public services, norms, or long-term conditions.
Typical signals: price increases from supply shocks; tax/budget impacts; broader labor market effects; spillover health risks; policy spillovers; general public affected via macroeconomic or environmental shifts.

# C) Already affected vs. Potential future
- Already affected: effects are occurring *now or in the past* (implemented, active, observed, or measurable).
- Potential future: effects are *plausible but not yet realized*, contingent on adoption, enforcement, scaling, follow-on decisions, or uncertain forecasts.

# D) Counting & consistency
- Units are "millions of people" (e.g., 2.5 = 2,500,000).
- Prefer conservative estimates; if unsure, round down.
- Avoid double-counting: the same people should not be counted in both direct and indirect for the same timeframe (choose the strongest category; default to direct).
- If the article provides no credible basis for a number, use 0.0 and note why in the "error" field.
- For broad "everyone might be affected someday" claims, only count indirect future if there is a concrete mechanism (policy, technology deployment, market shift) described.

# Key Notes

- Focus on global, long-term impact on humanity.
- Prioritize significance for positive and scientific news.
- Heavily negative or casualty-heavy news should have commensurately lower significance scores.
- Ongoing wars with no major new developments: cap at 0.3 significance.
- Localized events: no higher than 0.2.
- Accurately assess stakeholder impact.
- Reference full definitions for every classification.
- Assign relevance 0.0 for podcasts, videos, or other non-article inputs.
- If uncertain, use the lower end of the scale.
- Determine significance only during the final step.

After completing all steps, review your output to ensure the rationale and output fields are consistent, complete, and justified by your analysis. If output is ambiguous or incomplete, self-correct before finalizing.`

// classifyContract is appended to every prompt (user-defined or default) so
// the output stays machine-readable. Analysis text may precede the JSON (the
// default prompt opens with a checklist), but nothing may follow it.
const classifyContract = `

Output contract (binding): Finish your reply with exactly ONE JSON object in this shape, with no markdown fence and nothing after it:
{"evaluationReflectionParagraph": "<string; minimum one sentence explanation>", "potentialFutureDirectlyAffectedHumansInMillions": <float; 0.0 if unknown>, "potentialFutureIndirectlyAffectedHumansInMillions": <float; 0.0 if unknown>, "alreadyDirectlyAffectedHumansInMillions": <float; 0.0 if unknown>, "alreadyIndirectlyAffectedHumansInMillions": <float; 0.0 if unknown>, "significance": <float; between 0.0 and 1.0>, "negativity": <float; 0.0 = positive, 0.5 = neutral, 1.0 = negative>, "personalRelevance": <float; between 0.0 and 1.0, per personal context>, "tags": [up to 10 string tags]}`

// classifier is the seam between the pipeline and the LLM backend; tests
// replace newClassifier with a stub.
type classifier interface {
	Classify(system, user string) (string, error)
	BeginUse() func()
}

type llmClassifier struct {
	client *llm.ChatClient
}

func (l llmClassifier) Classify(system, user string) (string, error) {
	return l.client.Complete([]llm.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
}

// BeginUse holds the backend lease so the model is not idle-stopped mid-run.
func (l llmClassifier) BeginUse() func() {
	return llm.BeginBackendUse(l.client)
}

// newClassifier is the production seam for tests. The classifier resolves
// through the app's single llm_inferencer note (a singleton — one global LLM
// provider configuration): when it binds the "rss_classify" usage
// (OpenRouter / Mistral / any OpenAI-compatible provider, with the
// note-stored API key), that provider wins; a {local: true} binding or no
// binding at all falls back to the config.toml feature/tier system client
// (the local LLM). The prompt is built from note bodies (the rss_prompt
// note's body, the reader note's rules scope), so resolution goes through
// the privacy guard: when the reader or prompt note is marked high_private,
// a remote binding is downgraded to the local LLM for the call. On resolver
// failure the config client is used so a broken inferencer note degrades
// gracefully rather than blocking classification.
var newClassifier = func(db *sql.DB, readerNoteID, promptNoteID int64) classifier {
	client, _, err := llminferencer.ResolveChatForNotes(db, "rss_classify", readerNoteID, promptNoteID)
	if err != nil || client == nil {
		return llmClassifier{client: llm.NewRSSClassifierClient()}
	}
	return llmClassifier{client: client}
}

// classifyUserMessage builds the user message for one article.
func classifyUserMessage(title, feedName, url, content string) string {
	if content == "" {
		content = "(no content available)"
	}
	if utf8.RuneCountInString(content) > classifyMaxRunes {
		content = string([]rune(content)[:classifyMaxRunes]) + "…"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Article title: %s\n", title)
	if feedName != "" {
		fmt.Fprintf(&b, "Feed: %s\n", feedName)
	}
	if url != "" {
		fmt.Fprintf(&b, "URL: %s\n", url)
	}
	b.WriteString("\n")
	b.WriteString(content)
	return b.String()
}

// parseClassification decodes the LLM reply, clipping fields into sane
// bounds so bad prompts cannot poison the schema. Missing evaluation
// dimensions default to their zero values. The legacy `summary` key is
// still parsed (custom pre-evaluation prompts may emit it) but discarded:
// the evaluation never produces a summary — that is the summarizer pass's
// job.
func parseClassification(raw string) (Classification, error) {
	var out Classification
	if err := llm.UnmarshalLooseJSONObject(raw, &out); err != nil {
		return Classification{}, err
	}

	out.EvaluationReflectionParagraph = strings.TrimSpace(out.EvaluationReflectionParagraph)
	if utf8.RuneCountInString(out.EvaluationReflectionParagraph) > 2000 {
		out.EvaluationReflectionParagraph = string([]rune(out.EvaluationReflectionParagraph)[:2000])
	}
	out.Significance = clamp01(out.Significance)
	out.Negativity = clamp01(out.Negativity)
	out.PersonalRelevance = clamp01(out.PersonalRelevance)
	out.AlreadyDirectlyAffectedHumansInMillions = clampHumans(out.AlreadyDirectlyAffectedHumansInMillions)
	out.AlreadyIndirectlyAffectedHumansInMillions = clampHumans(out.AlreadyIndirectlyAffectedHumansInMillions)
	out.PotentialFutureDirectlyAffectedHumansInMillions = clampHumans(out.PotentialFutureDirectlyAffectedHumansInMillions)
	out.PotentialFutureIndirectlyAffectedHumansInMillions = clampHumans(out.PotentialFutureIndirectlyAffectedHumansInMillions)

	out.Error = strings.TrimSpace(out.Error)
	if utf8.RuneCountInString(out.Error) > 500 {
		out.Error = string([]rune(out.Error)[:500])
	}

	// Legacy fields (kept for custom prompts predating the evaluation
	// contract).
	out.Category = strings.TrimSpace(out.Category)
	if utf8.RuneCountInString(out.Category) > 100 {
		out.Category = string([]rune(out.Category)[:100])
	}
	out.Summary = strings.TrimSpace(out.Summary)
	if utf8.RuneCountInString(out.Summary) > 1200 {
		out.Summary = string([]rune(out.Summary)[:1200])
	}
	if out.Quality < 0 {
		out.Quality = 0
	} else if out.Quality > 10 {
		out.Quality = 10
	}

	if out.Tags == nil {
		out.Tags = []string{}
	}
	if len(out.Tags) > 10 {
		out.Tags = out.Tags[:10]
	}
	for i, tag := range out.Tags {
		tag = strings.TrimSpace(tag)
		if utf8.RuneCountInString(tag) > 60 {
			tag = string([]rune(tag)[:60])
		}
		out.Tags[i] = tag
	}
	return out, nil
}

// classifyOnce runs one classification attempt with the given system prompt;
// invalid output is retried once with the parse error appended.
func classifyOnce(c classifier, system, user string) (Classification, string, error) {
	raw, err := c.Classify(system, user)
	if err != nil {
		return Classification{}, "", err
	}
	out, perr := parseClassification(raw)
	if perr != nil {
		// One corrective retry.
		retrySystem := system + fmt.Sprintf("\n\nYour previous reply was invalid: %v. Reply again correctly.", perr)
		raw2, err := c.Classify(retrySystem, user)
		if err != nil {
			return Classification{}, raw, err
		}
		out, perr = parseClassification(raw2)
		if perr != nil {
			return Classification{}, raw2, fmt.Errorf("invalid model output: %w", perr)
		}
		return out, raw2, nil
	}
	return out, raw, nil
}

// itemForClassify is a pending classification work row.
type itemForClassify struct {
	ID      int64
	Title   string
	URL     string
	Content string  // full_content || feed_content
	Penalty float64 // current rule penalty score
}

// loadPendingItems selects up to classifyBatchSize pending items for a note,
// oldest first.
func loadPendingItems(db *sql.DB, noteID int64) ([]itemForClassify, error) {
	rows, err := db.Query(`
		SELECT i.id, i.title, i.url, COALESCE(NULLIF(i.full_content,''), i.feed_content, ''), i.penalty_score
		FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ? AND i.classify_status = 'pending'
		ORDER BY i.id
		LIMIT ?`, noteID, classifyBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("rss: classify: load pending: %w", err)
	}
	defer rows.Close()
	var items []itemForClassify
	for rows.Next() {
		var it itemForClassify
		if err := rows.Scan(&it.ID, &it.Title, &it.URL, &it.Content, &it.Penalty); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// storeClassification persists a successful classification: the evaluation
// columns and the derived points (weights from the note's body rules, minus
// the item's rule penalties). The old summary column is CLEARED (discarded
// from the pipeline: summary_json from the summarizer pass is the only
// summary), so re-evaluating a legacy row drops its stale summary too. The
// prompt's optional "error" diagnostics land in classify_error while the
// status stays done.
func storeClassification(db *sql.DB, itemID, promptNoteID int64, c Classification, w ScoreWeights, penalty float64) error {
	tagsJSON, err := json.Marshal(c.Tags)
	if err != nil || len(tagsJSON) == 0 {
		tagsJSON = []byte("[]")
	}
	points := computePoints(c.metrics(), w, penalty)
	_, err = db.Exec(
		`UPDATE ct_rss_items
		 SET category = ?, summary = '', tags_json = ?, quality = ?,
		     significance = ?, negativity = ?, personal_relevance = ?,
		     affected_now_direct = ?, affected_now_indirect = ?,
		     affected_future_direct = ?, affected_future_indirect = ?,
		     reflection = ?, points = ?, evaluated = ?,
		     classify_status = 'done', classify_error = ?, prompt_note_id = ?
		 WHERE id = ?`,
		c.Category, string(tagsJSON), c.Quality,
		c.Significance, c.Negativity, c.PersonalRelevance,
		c.AlreadyDirectlyAffectedHumansInMillions, c.AlreadyIndirectlyAffectedHumansInMillions,
		c.PotentialFutureDirectlyAffectedHumansInMillions, c.PotentialFutureIndirectlyAffectedHumansInMillions,
		c.EvaluationReflectionParagraph, points, boolToInt(c.hasEvaluation()), c.Error, promptNoteID, itemID,
	)
	if err != nil {
		return fmt.Errorf("rss: classify: store item %d: %w", itemID, err)
	}
	return nil
}

// classificationWeights resolves the score weights for a reader: the body's
// `weights:` rules override the defaults.
func classificationWeights(db *sql.DB, noteID int64) ScoreWeights {
	body, err := noteBody(db, noteID)
	if err != nil {
		return defaultScoreWeights()
	}
	return parseRules(body).effectiveWeights()
}

// classifyProgressCount returns how many items are pending evaluation for a
// reader (the progress total at drain start).
func classifyProgressCount(db *sql.DB, noteID int64) int {
	var n int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM ct_rss_items i
		JOIN ct_rss_feeds f ON f.id = i.feed_id
		WHERE f.note_id = ? AND i.classify_status = 'pending'`, noteID,
	).Scan(&n)
	return n
}

// Progress stages streamed with the per-item drain updates: which pipeline
// stage the numbers belong to (evaluation drain vs. summarization drain).
const (
	progressStageEvaluate  = "evaluate"
	progressStageSummarize = "summarize"
)

// reportEvaluationProgress streams per-item drain progress to the UI (see
// notetype.ProgressReporter / the plugin.progress live messages). Fire and
// forget: no injected emitter (tests, tools) or a marshal error just skips
// the update. stage marks which drain is running ("evaluate"/"summarize").
func reportEvaluationProgress(noteID int64, stage string, done, total int, finished bool) {
	if progressFunc == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"note_id":  noteID,
		"stage":    stage,
		"done":     done,
		"total":    total,
		"finished": finished,
	})
	if err != nil {
		return
	}
	progressFunc(payload)
}

// classifyPendingItems processes pending items for a reader note and returns
// the number classified (done or errored). Batches drain until the queue is
// empty or the time budget is spent, so a full re-evaluation after a prompt
// change completes in one refresh run (the cron path repeats otherwise).
// Per-item progress is streamed to the UI when the progress emitter is
// injected.
func classifyPendingItems(db *sql.DB, noteID int64, cfg Config) int {
	if !cfg.AutoClassify {
		return 0
	}
	promptRef, prompt, err := promptText(db, noteID)
	if err != nil {
		return 0
	}
	weights := classificationWeights(db, noteID)

	cls := newClassifier(db, noteID, promptRef.NoteID)
	release := cls.BeginUse()
	defer release()

	system := prompt + classifyContract
	total := classifyProgressCount(db, noteID)
	if total == 0 {
		return 0
	}
	reportEvaluationProgress(noteID, progressStageEvaluate, 0, total, false)

	processed := 0
	drained := false
	deadline := time.Now().Add(classifyTimeBudget)
	for {
		items, err := loadPendingItems(db, noteID)
		if err != nil || len(items) == 0 {
			drained = err == nil
			break
		}
		for _, it := range items {
			user := classifyUserMessage(it.Title, "", it.URL, it.Content)
			classification, _, err := classifyOnce(cls, system, user)
			if err != nil {
				// Distinguish model failure from config failure: keep the item
				// visible with an error; the user can reclassify manually.
				msg := err.Error()
				if len(msg) > 500 {
					msg = msg[:500]
				}
				_, _ = db.Exec(
					`UPDATE ct_rss_items SET classify_status = 'error', classify_error = ? WHERE id = ?`,
					msg, it.ID,
				)
			} else if err := storeClassification(db, it.ID, promptRef.NoteID, classification, weights, it.Penalty); err != nil {
				_, _ = db.Exec(
					`UPDATE ct_rss_items SET classify_status = 'error', classify_error = ? WHERE id = ?`,
					err.Error(), it.ID,
				)
			}
			processed++
			reportEvaluationProgress(noteID, progressStageEvaluate, processed, total, false)
		}
		if len(items) < classifyBatchSize {
			drained = true
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	reportEvaluationProgress(noteID, progressStageEvaluate, processed, total, drained)
	return processed
}
