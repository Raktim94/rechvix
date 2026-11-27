require "minitest/autorun"
require "open3"
require "tmpdir"
require "yaml"

class IosSimulatorWorkflowTest < Minitest::Test
  WORKFLOW = YAML.load_file(File.expand_path("../../../.github/workflows/mobile.yml", __dir__))
  IOS = WORKFLOW.fetch("jobs").fetch("ios")

  def test_xcodebuild_status_reaches_the_runner
    build = IOS.fetch("steps").find { |step| step.fetch("run", "").include?("xcodebuild ") }
    shell = build["shell"] || IOS.dig("defaults", "run", "shell") || WORKFLOW.dig("defaults", "run", "shell")
    # Actions invokes implicit Bash with -e and explicit Bash with -eo pipefail.
    flags = {nil => %w[-e], "bash" => %w[--noprofile --norc -eo pipefail]}.fetch(shell)
    compiler = <<~'BASH'
      xcodebuild() { echo "simulated xcodebuild exit $COMPILER_STATUS"; return "$COMPILER_STATUS"; }
    BASH
    Dir.mktmpdir do |dir|
      Dir.mkdir(File.join(dir, "ios"))
      [0, 65].each do |code|
        env = {"COMPILER_STATUS" => code.to_s}
        output, result = Open3.capture2e(env, "bash", *flags, "-c", compiler + build.fetch("run"), chdir: dir)
        assert_equal code, result.exitstatus, output.strip
      end
    end
  end

  def test_the_simulator_artifact_is_required
    upload = IOS.fetch("steps").find { |step| step.fetch("uses", "").start_with?("actions/upload-artifact@") }
    assert_equal "apps/mobile/ios/build/Build/Products/Release-iphonesimulator/Rechvix.app", upload.fetch("with").fetch("path")
    assert_equal "error", upload.fetch("with")["if-no-files-found"], "a missing .app must fail the upload"
  end
end
