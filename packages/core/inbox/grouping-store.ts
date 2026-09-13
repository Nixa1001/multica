"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import {
  createWorkspaceAwareStorage,
  registerForWorkspaceRehydration,
} from "../platform/workspace-storage";
import { defaultStorage } from "../platform/storage";

interface InboxGroupingState {
  groupByProject: boolean;
  setGroupByProject: (enabled: boolean) => void;
}

export const useInboxGroupingStore = create<InboxGroupingState>()(
  persist(
    (set) => ({
      groupByProject: false,
      setGroupByProject: (groupByProject) => set({ groupByProject }),
    }),
    {
      name: "multica_inbox_grouping",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (state) => ({ groupByProject: state.groupByProject }),
    },
  ),
);

registerForWorkspaceRehydration(() => useInboxGroupingStore.persist.rehydrate());
