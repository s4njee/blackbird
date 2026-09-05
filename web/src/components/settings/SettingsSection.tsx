// SettingsSection dispatcher (POL-8.8): routes the active section name to
// its section component. One file per section; this file only branches.
import { createMemo } from "solid-js";
import type { SectionProps } from "./types";
import { isInterface } from "./model";
import { makeFields } from "./fields";
import { AboutSection } from "./AboutSection";
import { AdvancedSection } from "./AdvancedSection";
import { AutomationSection } from "./AutomationSection";
import { BandwidthSection } from "./BandwidthSection";
import { ConnectionSection } from "./ConnectionSection";
import { DirectoriesSection } from "./DirectoriesSection";
import { GeneralSection } from "./GeneralSection";
import { HistorySection } from "./HistorySection";
import { InterfaceSection } from "./InterfaceSection";
import { LabelsSection } from "./LabelsSection";
import { QueueSection } from "./QueueSection";
import { SchedulerSection } from "./SchedulerSection";
import { SeedingSection } from "./SeedingSection";

export function SettingsSection(props: SectionProps) {
  // BandwidthSection takes a prebuilt input renderer instead of raw
  // updaters; every other section receives the full props.
  const { input } = makeFields(props);
  const body = createMemo(() => {
    if (isInterface(props.active)) return <InterfaceSection {...props} />;
    if (props.active === "General") return <GeneralSection />;
    if (props.active === "Connection") return <ConnectionSection {...props} />;
    if (props.active === "Bandwidth")
      return (
        <BandwidthSection
          draft={props.draft}
          errors={props.errors}
          setDraft={props.setDraft}
          input={input}
        />
      );
    if (props.active === "Seeding")
      return (
        <SeedingSection
          draft={props.draft}
          errors={props.errors}
          setDraft={props.setDraft}
          updateSeedingGroup={props.updateSeedingGroup}
          addSeedingGroup={props.addSeedingGroup}
          removeSeedingGroup={props.removeSeedingGroup}
        />
      );
    if (props.active === "Scheduler")
      return (
        <SchedulerSection
          draft={props.draft}
          errors={props.errors}
          setDraft={props.setDraft}
          updateScheduleProfile={props.updateScheduleProfile}
          addScheduleProfile={props.addScheduleProfile}
          removeScheduleProfile={props.removeScheduleProfile}
          paintScheduleCell={props.paintScheduleCell}
        />
      );
    if (props.active === "Queue") return <QueueSection {...props} />;
    if (props.active === "Directories") return <DirectoriesSection {...props} />;
    if (props.active === "Labels") return <LabelsSection {...props} />;
    if (props.active === "Automation")
      return (
        <AutomationSection
          draft={props.draft}
          errors={props.errors}
          updateRule={props.updateRule}
          addRule={props.addRule}
          removeRule={props.removeRule}
          updateUnpackRule={props.updateUnpackRule}
          addUnpackRule={props.addUnpackRule}
          removeUnpackRule={props.removeUnpackRule}
          updateFeed={props.updateFeed}
          addFeed={props.addFeed}
          removeFeed={props.removeFeed}
          updateFilter={props.updateFilter}
          addFilter={props.addFilter}
          removeFilter={props.removeFilter}
        />
      );
    if (props.active === "History") return <HistorySection {...props} />;
    if (props.active === "About") return <AboutSection />;
    return <AdvancedSection {...props} />;
  });
  return <>{body()}</>;
}
