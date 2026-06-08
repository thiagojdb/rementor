import { Component, createSignal, Show } from "solid-js";
import type { ApplicationDTO, WorkspaceDTO } from "../../api/types";
import StatusDot from "../ui/StatusDot";
import { toggleApplication } from "../../stores/workspaces";
import { toast } from "../../stores/toast";

interface AppCardProps {
  workspace: WorkspaceDTO;
  app: ApplicationDTO;
  onOpenDetail: (app: ApplicationDTO) => void;
}

const AppCard: Component<AppCardProps> = (props) => {
  const [toggling, setToggling] = createSignal(false);

  const isLocalApps = () => props.workspace.type === "local-apps";

  const handleToggle = async (e: MouseEvent) => {
    e.stopPropagation();
    if (toggling()) return;
    setToggling(true);
    try {
      await toggleApplication(props.workspace.id, props.app.id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Toggle failed");
    } finally {
      setToggling(false);
    }
  };

  const cardStyle = () => {
    const base: Record<string, string> = {
      "background-color": "var(--bg-secondary)",
      border: "1px solid var(--border-subtle)",
      "border-left": "3px solid transparent",
    };
    if (isLocalApps()) {
      // For local-apps: color by health status only
      if (props.app.healthStatus === "healthy") {
        base["border-left"] = "3px solid var(--accent-500)";
      } else if (props.app.healthStatus === "unhealthy") {
        base["border-left"] = "3px solid var(--error)";
      }
    } else {
      if (props.app.active) {
        base["border-left"] = "3px solid var(--accent-500)";
      } else if (props.app.healthStatus === "unhealthy") {
        base["border-left"] = "3px solid var(--error)";
      }
    }
    return base;
  };

  return (
    <div
      class="rounded-lg p-4 cursor-pointer transition-all hover:shadow-lg"
      style={{
        ...cardStyle(),
      }}
      onClick={() => props.onOpenDetail(props.app)}
    >
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2 mb-1">
            <StatusDot
              status={props.app.healthStatus}
              title={`Local: ${props.app.healthStatus}`}
            />
            <h3
              class="font-mono font-bold text-sm truncate"
              style={{ color: "var(--text-primary)" }}
            >
              {props.app.name}
            </h3>
          </div>
          <div class="flex gap-2 flex-wrap">
            <span
              class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
              style={{
                "background-color": "var(--bg-tertiary)",
                color: "var(--text-secondary)",
              }}
            >
              {isLocalApps()
                ? (props.app.domain ?? props.app.path)
                : props.app.path}
            </span>
            <Show when={!isLocalApps() && props.app.domain}>
              <span
                class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold"
                style={{
                  border: "1px solid var(--border-subtle)",
                  color: "var(--accent-400)",
                }}
              >
                {props.app.domain}
              </span>
            </Show>
            {props.app.port > 0 && (
              <span
                class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide"
                style={{
                  border: "1px solid var(--border-subtle)",
                  color: "var(--text-tertiary)",
                }}
              >
                :{props.app.port}
              </span>
            )}
          </div>
        </div>

        {/* Toggle Switch — only for routing workspaces */}
        <Show when={!isLocalApps()}>
          <button
            class={`relative inline-flex h-6 w-11 items-center rounded-full border transition-colors ${
              props.app.active ? "switch-checked" : "switch-unchecked"
            } ${!props.app.port ? "opacity-35 cursor-not-allowed" : ""}`}
            style={
              props.app.active
                ? {
                    "background-color": "var(--accent-500)",
                    "border-color": "var(--accent-500)",
                  }
                : {
                    "background-color": "var(--bg-tertiary)",
                    "border-color": "var(--border-default)",
                  }
            }
            disabled={!props.app.port || toggling()}
            onClick={handleToggle}
            title={props.app.active ? "Switch to remote" : "Switch to local"}
          >
            <span class="switch-thumb" />
          </button>
        </Show>
      </div>

      {/* Remote status row — only for routing workspaces */}
      <Show when={!isLocalApps()}>
        <div class="flex items-center gap-1.5 mt-3">
          <StatusDot
            status={props.app.remoteStatus ?? "unknown"}
            title={`Remote: ${props.app.remoteStatus ?? "unknown"}`}
          />
          <span class="text-[11px]" style={{ color: "var(--text-tertiary)" }}>
            {props.app.active ? "local" : "remote"}
          </span>
          {props.app.active && (
            <span
              class="ml-auto inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wide"
              style={{
                "background-color": "var(--accent-subtle)",
                color: "var(--accent-400)",
              }}
            >
              LOCAL
            </span>
          )}
        </div>
      </Show>
    </div>
  );
};

export default AppCard;
