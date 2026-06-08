import { Component, For, Show } from "solid-js";
import type { ApplicationDTO, WorkspaceDTO } from "../../api/types";
import AppTableRow from "./AppTableRow";

interface AppTableProps {
  workspace: WorkspaceDTO;
  apps: ApplicationDTO[];
  onOpenDetail: (app: ApplicationDTO) => void;
}

const AppTable: Component<AppTableProps> = (props) => {
  const isLocalApps = () => props.workspace.type === "local-apps";
  // colspan: 6 for routing (app, route, port, remote, status, toggle), 4 for local-apps (app, domain, port, status)
  const colspan = () => (isLocalApps() ? 4 : 6);

  return (
    <div
      class="overflow-x-auto rounded-lg"
      style={{
        "background-color": "var(--bg-secondary)",
        border: "1px solid var(--border-subtle)",
      }}
    >
      <table class="w-full border-collapse">
        <thead>
          <tr style={{ "border-bottom": "1px solid var(--border-subtle)" }}>
            <th
              class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider first:rounded-tl-lg"
              style={{
                color: "var(--text-tertiary)",
                "background-color": "var(--bg-secondary)",
              }}
            >
              Application
            </th>
            <th
              class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider"
              style={{
                color: "var(--text-tertiary)",
                "background-color": "var(--bg-secondary)",
              }}
            >
              {isLocalApps() ? "Domain" : "Route"}
            </th>
            <th
              class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider"
              style={{
                color: "var(--text-tertiary)",
                "background-color": "var(--bg-secondary)",
              }}
            >
              Port
            </th>
            <Show when={!isLocalApps()}>
              <th
                class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider"
                style={{
                  color: "var(--text-tertiary)",
                  "background-color": "var(--bg-secondary)",
                }}
              >
                Remote
              </th>
            </Show>
            <th
              class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider"
              style={{
                color: "var(--text-tertiary)",
                "background-color": "var(--bg-secondary)",
              }}
            >
              Status
            </th>
            <Show when={!isLocalApps()}>
              <th
                class="text-left py-2.5 px-4 font-mono font-semibold text-[11px] uppercase tracking-wider last:rounded-tr-lg"
                style={{
                  color: "var(--text-tertiary)",
                  "background-color": "var(--bg-secondary)",
                }}
              >
                Toggle
              </th>
            </Show>
          </tr>
        </thead>
        <tbody>
          <For each={props.apps}>
            {(app) => (
              <AppTableRow
                workspace={props.workspace}
                app={app}
                onOpenDetail={props.onOpenDetail}
              />
            )}
          </For>
          <Show when={props.apps.length === 0}>
            <tr>
              <td
                colspan={colspan()}
                class="py-12 text-center font-mono text-sm"
                style={{ color: "var(--text-tertiary)" }}
              >
                No applications to show
              </td>
            </tr>
          </Show>
        </tbody>
      </table>
    </div>
  );
};

export default AppTable;
