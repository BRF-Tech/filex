/**
 * @brftech/filex-core — public types.
 *
 * Types are split across two files for legacy reasons; this barrel
 * re-exports the lot so consumers only ever need the package root.
 */

export type {
  ExplorerConfig,
  ExplorerEmits,
  AuthConfig,
  ThemeMode,
  LocaleCode,
  EndpointMap,
} from './ExplorerConfig';

export type {
  FileNode,
  ShareInfo,
  UploadLimits,
  Capabilities,
  UploadInitResponse,
  UploadFinalizeResponse,
  ArchiveEntry,
  ViewMode,
  ClipboardState,
} from './FileNode';
