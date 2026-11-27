require "json"
require "minitest/autorun"
require "open3"
require "yaml"

class DockerPublishTest < Minitest::Test
  WORKFLOW = YAML.load_file(File.expand_path("../.github/workflows/docker-publish.yml", __dir__))
  SHA = "51802ac25e7d9ea9cc402ba09c522d3a19c7a007"

  def test_both_images_require_ci
    publish = WORKFLOW.fetch("jobs").fetch("publish")
    assert_includes Array(publish["needs"]), "verify-ci", "publishing can start before CI"
  end

  def test_waits_for_the_matching_ci_run_to_succeed
    output, status = check_ci(nil, ["queued", nil], ["in_progress", nil], ["completed", "success"])
    assert status.success?, output
  end

  def test_unsuccessful_conclusions_block_publication
    %w[failure cancelled skipped timed_out neutral].each do |conclusion|
      output, status = check_ci(["completed", conclusion])
      refute status.success?, "accepted #{conclusion}: #{output}"
    end
  end

  def test_missing_ci_blocks_publication
    output, status = check_ci(nil)
    refute status.success?, output
  end

  def test_ci_that_never_finishes_blocks_publication
    output, status = check_ci(["in_progress", nil])
    refute status.success?, output
    assert_match(/Timed out/, output)
  end

  private

  # Run the workflow's actual shell step. Each GitHub API call consumes one
  # response from stdin, so queue transitions take no wall-clock time.
  def check_ci(*runs)
    steps = WORKFLOW.fetch("jobs").fetch("verify-ci").fetch("steps")
    script = steps.fetch(0).fetch("run")
    responses = runs.map do |run|
      rows = run ? [{status: run[0], conclusion: run[1], html_url: "https://example.test/ci/1"}] : []
      JSON.generate(workflow_runs: rows)
    end
    stub = <<~'BASH'
      gh() {
        [[ "$1" == api && "$2" == "$EXPECTED_ENDPOINT" && "$3" == --jq ]] || return 2
        local response
        IFS= read -r response || response="$LAST_RESPONSE"
        jq -r "$4" <<< "$response"
      }
      sleep() { :; }
    BASH
    env = {
      "GITHUB_REPOSITORY" => "sample/repo", "GITHUB_SHA" => SHA,
      "LAST_RESPONSE" => responses.last,
      "EXPECTED_ENDPOINT" => "repos/sample/repo/actions/workflows/ci.yml/runs?branch=main&event=push&head_sha=#{SHA}&per_page=1"
    }
    Open3.capture2e(env, "bash", "-eo", "pipefail", "-c", stub + script, stdin_data: responses.join("\n") + "\n")
  end
end
