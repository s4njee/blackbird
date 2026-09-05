// Queue section (POL-8.8).
import type { SectionProps } from "./types";
import { makeFields } from "./fields";

export function QueueSection(props: SectionProps) {
  const { input } = makeFields(props);
  return (
    <section>
      <h1>Queue</h1>
      <div class="settings-fields">
        {input("max_downloads_global", "Max active downloads", "throttle.max_downloads.global")}
        {input("max_uploads_global", "Max active uploads", "throttle.max_uploads.global")}
      </div>
      <p class="settings-intro">
        Stop-on-ratio and seeding-time policy live under Seeding (ratio groups), evaluated per poll
        cycle. The caps above bound global activity.
      </p>
    </section>
  );
}
