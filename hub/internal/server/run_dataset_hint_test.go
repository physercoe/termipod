package server

import "testing"

// The configs below are the shapes LeRobot really produces. `dataset` is a
// `DatasetConfig` with `repo_id: str` and `root: str | None`
// (src/lerobot/configs/default.py); `TrainPipelineConfig` holds it beside
// `policy`, which has a `repo_id` of its own that means something else
// entirely.

func TestDatasetHintReadsTheNestedTrainConfig(t *testing.T) {
	h := datasetHintFromConfig([]byte(`{
		"dataset": {"repo_id": "lerobot/pusht", "episodes": null},
		"policy": {"type": "act", "repo_id": "me/act-pusht"},
		"steps": 100000
	}`))
	if h == nil {
		t.Fatal("no hint from a config that names a dataset")
	}
	if h.Value != "lerobot/pusht" || h.Kind != "repo_id" {
		t.Fatalf("hint = %+v", h)
	}
	if h.Key != "dataset.repo_id" {
		t.Errorf("key = %q, want dataset.repo_id", h.Key)
	}
}

func TestDatasetHintReadsAFlattenedConfig(t *testing.T) {
	// trackio and wandb both flatten nested config to dotted keys. Reading
	// only the nested shape means every run logged through a tracker produces
	// no hint at all, which looks identical to "this run has no dataset".
	h := datasetHintFromConfig([]byte(`{
		"dataset.repo_id": "lerobot/aloha_sim_insertion_human",
		"policy.repo_id": "me/diffusion",
		"output_dir": "outputs/train/x"
	}`))
	if h == nil || h.Value != "lerobot/aloha_sim_insertion_human" {
		t.Fatalf("hint = %+v", h)
	}
	if h.Key != "dataset.repo_id" {
		t.Errorf("key = %q", h.Key)
	}
}

func TestDatasetHintPrefersARootPathOverARepoID(t *testing.T) {
	// A root is a location, which is exactly what a dataset row is keyed by. A
	// repo id has to be matched against a path suffix, which is weaker — so
	// when a config carries both, the strong one wins.
	h := datasetHintFromConfig([]byte(`{
		"dataset": {"repo_id": "lerobot/pusht", "root": "/data/lerobot/pusht"}
	}`))
	if h == nil || h.Kind != "path" || h.Value != "/data/lerobot/pusht" {
		t.Fatalf("hint = %+v", h)
	}
}

func TestDatasetHintIgnoresThePolicyRepoID(t *testing.T) {
	// `policy.repo_id` is the MODEL. It sits one key away from the dataset in
	// every training config, and sniffing it would link every run to a dataset
	// that does not exist — a wrong edge that sends someone to watch the wrong
	// robot and believe what they see.
	for _, cfg := range []string{
		`{"policy": {"repo_id": "me/act-pusht"}, "steps": 1}`,
		`{"policy.repo_id": "me/act-pusht"}`,
		`{"policy.pretrained_path": "/ckpt/act"}`,
	} {
		if h := datasetHintFromConfig([]byte(cfg)); h != nil {
			t.Errorf("%s → %+v, want no hint", cfg, h)
		}
	}
}

func TestDatasetHintTakesTheFirstOfAMultiDatasetRun(t *testing.T) {
	// LeRobot's own config validation branches on `isinstance(repo_id, list)`,
	// so this shape is supported upstream. One run then has several datasets,
	// which a single dataset_id column cannot express — proposing the first is
	// honest as a starting point, and the user confirms or replaces it.
	h := datasetHintFromConfig([]byte(`{"dataset": {"repo_id": ["lerobot/a", "lerobot/b"]}}`))
	if h == nil || h.Value != "lerobot/a" {
		t.Fatalf("hint = %+v", h)
	}
}

func TestDatasetHintFindsAnEvalRecordingTarget(t *testing.T) {
	// Where an eval run WRITES its rollouts, as opposed to what it evaluates
	// against. This is the field that makes "watch what this eval actually
	// did" possible at all.
	h := datasetHintFromConfig([]byte(`{"eval": {"recording": true, "recording_repo_id": "me/eval_rollouts"}}`))
	if h == nil || h.Value != "me/eval_rollouts" || h.Key != "eval.recording_repo_id" {
		t.Fatalf("hint = %+v", h)
	}
}

func TestDatasetHintDeclinesRatherThanGuesses(t *testing.T) {
	// Most runs are not about a dataset. Silence is the correct answer, and
	// each of these could be coaxed into a plausible wrong one.
	for _, cfg := range []string{
		``,
		`not json`,
		`[]`,
		`{}`,
		`{"dataset": {"repo_id": ""}}`,
		`{"dataset": {"repo_id": "   "}}`,
		`{"dataset": "lerobot/pusht"}`, // a string where the config nests an object
		`{"dataset": {"repo_id": 42}}`,
		`{"repo_id": "lerobot/pusht"}`, // bare and unqualified — could be the policy
	} {
		if h := datasetHintFromConfig([]byte(cfg)); h != nil {
			t.Errorf("%s → %+v, want no hint", cfg, h)
		}
	}
}

func TestDatasetHintIsCaseInsensitiveAboutKeys(t *testing.T) {
	h := datasetHintFromConfig([]byte(`{"Dataset": {"Repo_ID": "lerobot/pusht"}}`))
	if h == nil || h.Value != "lerobot/pusht" {
		t.Fatalf("hint = %+v", h)
	}
}

func TestDatasetMatchesAPathHintExactly(t *testing.T) {
	h := DatasetHint{Value: "/data/lerobot/pusht", Kind: "path"}
	// Trailing separators are not part of a path's identity — the hub stores
	// the root string as sent, so the same dataset is registered both ways.
	for _, root := range []string{"/data/lerobot/pusht", "/data/lerobot/pusht/", "/data/lerobot/pusht//"} {
		if !datasetMatchesHint(h, root, "") {
			t.Errorf("%q should match", root)
		}
	}
	// A prefix is NOT a match: the parent directory of a dataset holds other
	// datasets, and linking a run to the wrong sibling is worse than no link.
	for _, root := range []string{"/data/lerobot", "/data/lerobot/pusht_v2", "/other/lerobot/pusht"} {
		if datasetMatchesHint(h, root, "") {
			t.Errorf("%q should not match", root)
		}
	}
}

func TestDatasetMatchesARepoIDByNameOrCachePath(t *testing.T) {
	h := DatasetHint{Value: "lerobot/pusht", Kind: "repo_id"}
	if !datasetMatchesHint(h, "/anything", "lerobot/pusht") {
		t.Error("the dataset's own name should match")
	}
	// $HF_LEROBOT_HOME/<org>/<name> is where the cache puts a downloaded repo.
	if !datasetMatchesHint(h, "/home/me/.cache/huggingface/lerobot/lerobot/pusht", "") {
		t.Error("a cache path ending in the repo id should match")
	}
	if !datasetMatchesHint(h, `C:\data\lerobot\pusht`, "") {
		t.Error("a Windows root should match on the same rule")
	}
	// Only the LAST TWO segments, so two orgs' datasets of the same name stay
	// distinct — matching the trailing segment alone would confuse them.
	if datasetMatchesHint(h, "/data/someone_else/pusht", "") {
		t.Error("a different org's dataset of the same name must not match")
	}
	if datasetMatchesHint(h, "/data/pusht", "") {
		t.Error("the bare name is not the repo id")
	}
	if datasetMatchesHint(DatasetHint{Value: "", Kind: "repo_id"}, "/data/pusht", "") {
		t.Error("an empty hint matches nothing")
	}
}
