import { isLosslessNumber, parse, stringify } from 'lossless-json'

export class APIError extends Error {
  readonly code: string
  readonly status: number
  constructor(
    code: string,
    message: string,
    status = 0,
    options?: ErrorOptions,
  ) {
    super(message, options)
    this.name = 'APIError'
    this.code = code
    this.status = status
  }
}

const messages: Record<string, string> = {
  WEB_AUTHORIZATION_REQUIRED: 'Sign in to the web interface.',
  NATIVE_AUTHORIZATION_REQUIRED: 'Sign in to CrashPlan to use this control.',
  NATIVE_OPERATION_INDETERMINATE:
    'The action may have started. Check its status before trying again.',
  NATIVE_LOGIN_REJECTED:
    'CrashPlan rejected the sign-in. Check your account details and server availability.',
  CONTROL_BUSY: 'The control service is busy. No action was queued.',
  SOURCE_NOT_MOUNTED: 'A configured source dataset is not mounted.',
  SOURCE_FILESYSTEM_REJECTED:
    'A configured source uses an unsupported filesystem.',
  SOURCE_SENTINEL_MISSING: 'The configured source marker is missing.',
  SOURCE_IDENTITY_CHANGED: 'A source changed since the engine started.',
  SCAN_JOURNAL_UNAVAILABLE:
    'The scan journal is unavailable. Backup supervision continues.',
  PRIVATE_STATE_UNAVAILABLE: 'The private application state is unavailable.',
  ENGINE_NOT_RUNNING: 'The engine is not running.',
  LOGIN_ATTEMPT_ALREADY_COMPLETED: 'This sign-in attempt has ended.',
  NATIVE_OPERATION_NOT_DISPATCHED:
    'The native action was not sent because the connection failed.',
  ARCHIVE_STATISTICS_PENDING: 'A previous archive count is running or unresolved. Request its progress before starting another count.',
  NATIVE_RESPONSE_PENDING:
    'A previous native response is unresolved. Restart the engine before repeating this operation.',
  SOURCE_NOT_READY: 'A configured local source is unavailable.',
  SCAN_ALREADY_PENDING: 'A scan is already pending for this backup set.',
  NATIVE_SESSION_CHANGED:
    'The CrashPlan session changed. Review the current account before submitting this action again.',
  NATIVE_LOGIN_IN_PROGRESS: 'A CrashPlan sign-in is already in progress.',
  NATIVE_OPERATION_REJECTED: 'CrashPlan rejected the action.',
  NATIVE_PROTOCOL_MISMATCH:
    'This CrashPlan version returned an unexpected response.',
  NATIVE_ENGINE_UNAVAILABLE: 'The CrashPlan engine is not available.',
  CSRF_REJECTED: 'Reload the page to refresh your session.',
  PUBLIC_ORIGIN_MISMATCH:
    'The configured public address does not match this request.',
  WEB_LOGIN_RATE_LIMITED: 'Too many sign-in attempts. Try again shortly.',
  INVALID_REQUEST: 'Please check the entered values.',
  SETTING_LOCKED:
    'This setting is locked by the native policy. No settings were changed.',
  NATIVE_MFA_BROWSER_REQUIRED:
    'This account requires additional verification. Use browser SSO or the account console; native password login cannot submit an MFA code.',
  NATIVE_LOGIN_THROTTLED:
    'CrashPlan temporarily limited sign-in attempts. Try again later.',
  NATIVE_AUTHORITY_UNAVAILABLE:
    'The account authority is unavailable. Try sign-in again later.',
  NATIVE_REGISTRATION_REJECTED:
    'The registration key is invalid or account registration is blocked.',
  RESTORE_JOURNAL_UNAVAILABLE:
    'The restore journal is unavailable. New restores are disabled.',
  RESTORE_JOURNAL_FULL:
    'The restore journal has reached its limit. Existing jobs are still available.',
  RESTORE_DESTINATION_REJECTED:
    'Choose an existing directory inside a configured writable restore root.',
  RESTORE_DESTINATION_READ_ONLY: 'The restore destination is read-only.',
  RESTORE_MARKER_REJECTED:
    'The native permission check failed. Inspect existing jobs before submitting another restore.',
  RESTORE_PENDING_JOB_OUTSIDE_ROOTS:
    'This job is outside the configured restore roots. It cannot be resumed.',
  RESTORE_NO_SPACE: 'The restore destination does not have enough free space.',
  RESTORE_DUPLICATE_NAMES:
    'Restore selections with the same top-level name in separate jobs, or choose original-location restore.',
  ARCHIVE_UNLOCK_REQUIRED:
    'Unlock this archive in CrashPlan before browsing or restoring it.',
  ARCHIVE_SEARCH_PENDING:
    'A search is active or its outcome is unknown. An uncertain search remains blocked until the engine restarts.',
  ARCHIVE_SEARCH_SCOPE_UNSUPPORTED:
    'This archive supports archive-wide search. Search from the archive root.',
}

export function identifier(value: unknown): string {
  let candidate: string | undefined
  if (isLosslessNumber(value)) candidate = value.toString()
  else if (typeof value === 'string') candidate = value
  else if (typeof value === 'number' && Number.isSafeInteger(value))
    candidate = String(value)
  if (
    candidate &&
    /^[1-9]\d*$/.test(candidate) &&
    BigInt(candidate) <= 9223372036854775807n
  )
    return candidate
  throw new APIError(
    'INVALID_REQUEST',
    'A valid, exact positive identifier is required.',
  )
}

// Vendor KeyInfoV1 uses 56 raw bytes. CI checks this contract against its JAR.
export function generateArchiveKey(): string {
  return btoa(
    String.fromCharCode(...crypto.getRandomValues(new Uint8Array(56))),
  )
}

export type NativeObject = Record<string, unknown>
export interface Session {
  csrf: string
}
export interface LoginInfo {
  username: string
  serverAddress: string
  registrationKey?: string
  organizationKey?: string
  register?: boolean
  firstName?: string
  lastName?: string
}
export interface ScanOperation {
  id: string
  backupSetId: string
  state:
    | 'accepted'
    | 'running'
    | 'completed'
    | 'indeterminate'
    | 'stalled'
    | 'interrupted'
    | 'acknowledged'
    | 'failed'
  reason?: string
  completionEvidence?: 'native_recorded'
  engineVariant?: 'v1' | 'v3'
  baseline: unknown
  completion?: unknown
  created: string
  updated: string
}
export interface LoginResult {
  complete: boolean
  migrationCheck?: NativeObject
}
export interface ArchiveQuery {
  sourceId: string
  targetId: string
  backupSetId: string
  fileId: string
  at: string
  includeDeleted: boolean
  cursor?: string
  limit: number
}

export function archiveSearchQuery<T extends ArchiveQuery>(
  query: T,
  archiveType?: string,
): T {
  return archiveType === 'ARCHIVE_V3'
    ? { ...query, fileId: 'AAAAAAAAAAAAAAAAAAAAAA==' }
    : query
}
export interface LocalArchivePolicy {
  allowed: boolean
  ownDevices: boolean
  canAttachOwn?: boolean
  selfId: string
  devices: {
    id: string
    name: string
    own: boolean
    self: boolean
    parentId?: string
    connected?: boolean
    deleted?: boolean
  }[]
}
export interface ReplacementState {
  available: boolean
  inProgress: boolean
  deviceSelectedGuid: string
  screenState: string
}
export interface LocalArchiveRequest {
  requestId: string
  action: 'create' | 'attach' | 'move' | 'relocate'
  target: { rootId: string; relative: string }
  sourceId: string
  destinationId: string
  confirm: string
}
export interface DeviceOperationRequest {
  requestId: string
  action: 'rename' | 'remove' | 'maintenance'
  deviceId: string
  name?: string
  confirm: string
}
export interface LocalArchiveOperation {
  requestId: string
  state: string
  destinationId?: string
  jobId?: string
  existingArchive: boolean
  sourceMissing: boolean
  name?: string
  deleted?: boolean
}
export interface ArchiveVersion {
  timestamp: string
  modified: string
  size: string
  isFolder: boolean
}
export interface ArchiveItem extends ArchiveVersion {
  fileId: string
  path: string
}
export interface ArchivePage {
  incomplete: boolean
  items: ArchiveItem[]
  nextCursor?: string
}
export interface RestoreSelection {
  path: string
  backupSetId: string
  timestamp: string
  isFolder: boolean
}
export interface RestoreRequest {
  requestId: string
  sourceId: string
  targetId: string
  target: { rootId: string; relative: string }
  existing: string
  permissions: 'current' | 'original'
  includeDeleted: boolean
  items: RestoreSelection[]
  excluded?: RestoreSelection[]
}
export interface RestoreOperation {
  requestId: string
  generation: string
  restoreId?: string
  restorePath: string
  state: 'accepted' | 'rejected' | 'indeterminate'
  created: string
  children?: string[]
}
export interface RestoreRoot {
  id: string
  path: string
  writable: boolean
}
export interface RestoreJob {
  id: string
  sourceId: string
  targetId: string
  completed: boolean
  canceled: boolean
  numFiles: string
  filesRestored: string
  numBytes: string
  bytesRestored: string
  restorePath: string
  imported: boolean
  admissible: boolean
}
export interface Device {
  archiveType?: 'ARCHIVE_V1' | 'ARCHIVE_V3'
  id: string
  name: string
  source: boolean
  target: boolean
  availableTarget: boolean
  self: boolean
  blocked: boolean
  deleted: boolean
}
export interface MigrationDevice {
  computerGuid: string
  name: string
  hostname?: string
  osName?: string
  archiveBytes?: string
}
export interface MigrationState {
  status: string
  reason?: string
  devices: MigrationDevice[]
}
export type SecurityType = 'AccountPassword' | 'PrivatePassword' | 'CustomKey'
export interface SecurityState {
  type: SecurityType
  question?: string
}
export interface ArchiveUnlock {
  sourceId: string
  type: 'PrivatePassword' | 'CustomKey'
  secret?: string
  passphrase?: string
}
export interface ArchivePassword {
  action: 'create' | 'change' | 'reset' | 'question'
  currentPassword?: string
  currentAnswer?: string
  password?: string
  question?: string
  answer?: string
}
export interface SecurityUpgrade {
  type: 'PrivatePassword' | 'CustomKey'
  confirm: string
  password?: string
  question?: string
  answer?: string
  key?: string
  passphrase?: string
}
export interface DerivedKeyInput {
  sourceId?: string
  passphrase: string
}
export interface CustomKeyChange {
  key?: string
  passphrase?: string
  confirm: string
}
export interface BackupSummary {
  activities: number
  sendRateBytesPerSecond: string
  remainingBytes: string
  remainingFiles: number
  filesBackedUp: number
  lastBackup: string
  status: 'SAFE' | 'WARNING' | 'SEVERE'
  systemBusy: boolean
  batteryBlocked: boolean
}
export interface BackupControl {
  backupSetId?: string
  planUid?: string
  destinationGuid?: string
  durationMinutes?: string
}
export interface Alert {
  id: string
  name: string
  title?: string
  message?: string
  flags: number
  expiration: string
}
export interface HistoryEntry {
  level: string
  timestamp: string
  message: string
}
export type SettingsArea = 'backup' | 'network' | 'general' | 'ui' | 'peer'
export interface Setting {
  name: string
  label: string
  kind: string
  unit?: string
  value: unknown
  locked: boolean
  available: boolean
}
export interface SettingsResult {
  settings: Setting[]
  applied: Record<string, boolean>
  complete: boolean
}
export interface SelectionPath {
  path: string
  directory: boolean
  selected: boolean
}
export interface BackupSet {
  id: string
  locked: boolean
  fields: Setting[]
  paths: SelectionPath[]
  pathsLocked: boolean
  excludes: { pattern: string; regularExpression: boolean }[]
  excludesLocked: boolean
  destinations: { id: string; locked: boolean }[]
  destinationsLocked: boolean
  retention: Setting[]
}
export interface SetResult {
  set: BackupSet
  complete: boolean
  applied: Record<string, boolean>
}
export interface SourceItem {
  path: string
  directory: boolean
  exists: boolean
  alias: boolean
  selectionCode: 'SELECTED' | 'NOT_SELECTED' | 'EXCLUDED'
  hidden: boolean
  excludedForSystem: boolean
  excludedForUser: boolean
  excludedForHost: boolean
}
export interface SourcePage {
  items: SourceItem[]
  total: number
  offset: number
  parent: string
}

// Ordinary folder selection uses the archive's cutoff. The folder's own last
// version can predate newer files below it. Explicit file versions are separate.
export function archiveSelection(
  item: ArchiveItem,
  backupSetId: string,
  at: string,
): RestoreSelection {
  return {
    path: item.path,
    backupSetId,
    timestamp: at,
    isFolder: item.isFolder,
  }
}

// This client never sees the native HTTP token. Mutations never retry here.
export class API {
  private csrf = ''
  private engineContext = ''
  private engineChanged = false
  private readonly contextListeners = new Set<() => void>()
  private readonly base: string
  constructor(base = '') {
    this.base = base
  }

  subscribeContext = (listener: () => void): (() => void) => {
    this.contextListeners.add(listener)
    return () => { this.contextListeners.delete(listener) }
  }
  contextChanged = (): boolean => this.engineChanged

  private assertContext(): void {
    if (this.engineChanged)
      throw new APIError('NATIVE_SESSION_CHANGED', messages.NATIVE_SESSION_CHANGED!)
  }

  private observeContext(response: Response): void {
    const context = response.headers.get('X-CPH-Engine-Context')
    if (context && this.engineContext && context !== this.engineContext) {
      this.engineChanged = true
      for (const listener of this.contextListeners) listener()
    } else if (context && !this.engineContext) this.engineContext = context
    // Never retag an open form with a newer account. A full reload creates a new client.
    this.assertContext()
  }

  private async request<T>(
    path: string,
    method = 'GET',
    body?: unknown,
  ): Promise<T> {
    this.assertContext()
    let response: Response
    try {
      response = await fetch(this.base + path, {
        method,
        credentials: 'same-origin',
        cache: 'no-store',
        headers: {
          Accept: 'application/json',
          ...(method !== 'GET' ? { 'X-CPH-Engine-Context': this.engineContext } : {}),
          ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
          ...(method !== 'GET' && this.csrf
            ? { 'X-CSRF-Token': this.csrf }
            : {}),
        },
        ...(body !== undefined ? { body: stringify(body) } : {}),
      })
    } catch (cause) {
      throw new APIError(
        'CONNECTION_FAILED',
        'Cannot reach the service. The action was not automatically repeated.',
        0,
        { cause },
      )
    }
    this.observeContext(response)
    let envelope: { data?: T; error?: { code?: string } }
    try {
      envelope = parse(await response.text()) as typeof envelope
    } catch (cause) {
      throw new APIError(
        'INVALID_RESPONSE',
        'The service returned an invalid response. Check its status before repeating an action.',
        response.status,
        { cause },
      )
    }
    if (!response.ok) {
      if (
        path === '/api/v1/health' &&
        response.status === 503 &&
        envelope?.data &&
        typeof envelope.data === 'object' &&
        'status' in envelope.data
      )
        return envelope.data as T
      const code = envelope?.error?.code
      if (typeof code === 'string' && Object.hasOwn(messages, code)) {
        throw new APIError(code, messages[code]!, response.status)
      }
      throw new APIError(
        'REQUEST_FAILED',
        'The request failed. Check the service logs.',
        response.status,
      )
    }
    if (
      !envelope ||
      typeof envelope !== 'object' ||
      !Object.hasOwn(envelope, 'data')
    ) {
      throw new APIError(
        'INVALID_RESPONSE',
        'The service did not return the expected result.',
        response.status,
      )
    }
    return envelope.data as T
  }

  async session(passive = false): Promise<Session> {
    const session = await this.request<Session>(
      passive ? '/api/v1/session/status' : '/api/v1/session',
    )
    this.csrf = session.csrf
    return session
  }
  async signIn(username: string, password: string): Promise<Session> {
    const session = await this.request<Session>('/api/v1/session', 'POST', {
      username,
      password,
    })
    this.csrf = session.csrf
    return session
  }
  async signOut(): Promise<void> {
    await this.request('/api/v1/session', 'DELETE')
    this.csrf = ''
  }
  status<T = NativeObject>(): Promise<T> {
    return this.request<T>('/api/v1/state')
  }
  capabilities<T = NativeObject>(): Promise<T> {
    return this.request<T>('/api/v1/capabilities')
  }
  devices(): Promise<Device[]> {
    return this.request('/api/v1/devices')
  }
  security(): Promise<SecurityState> {
    return this.request('/api/v1/security')
  }
  exportArchiveKey(): Promise<Blob> {
    return this.downloadKey('/api/v1/security/export-key', {})
  }
  exportDerivedKey(input: DerivedKeyInput): Promise<Blob> {
    return this.downloadKey('/api/v1/security/derived-key/export', input)
  }
  replaceCustomKey(input: CustomKeyChange): Promise<SecurityState> {
    return this.request('/api/v1/security/custom-key', 'POST', input)
  }
  private async downloadKey(path: string, input: unknown): Promise<Blob> {
    this.assertContext()
    let response: Response
    try {
      response = await fetch(this.base + path, {
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': this.csrf,
          'X-CPH-Engine-Context': this.engineContext,
        },
        body: stringify(input),
      })
    } catch (cause) {
      throw new APIError('CONNECTION_FAILED', 'The key download failed.', 0, {
        cause,
      })
    }
    this.observeContext(response)
    if (!response.ok) {
      let envelope: { error?: { code?: string } }
      try {
        envelope = parse(await response.text()) as typeof envelope
      } catch (cause) {
        throw new APIError(
          'INVALID_RESPONSE',
          'The key download failed.',
          response.status,
          { cause },
        )
      }
      const code = envelope?.error?.code
      if (code && Object.hasOwn(messages, code))
        throw new APIError(code, messages[code]!, response.status)
      throw new APIError(
        'REQUEST_FAILED',
        'The key download failed.',
        response.status,
      )
    }
    if (response.headers.get('Content-Type') !== 'application/octet-stream')
      throw new APIError(
        'INVALID_RESPONSE',
        'The key download returned an unexpected response.',
      )
    return response.blob()
  }
  backupSummary(): Promise<BackupSummary> {
    return this.request('/api/v1/backup/summary')
  }
  backupControl(
    action: 'pause' | 'resume',
    input: BackupControl,
  ): Promise<{ accepted: boolean; backupSetIds: string[] }> {
    return this.request('/api/v1/backup/' + action, 'POST', input)
  }
  alerts(): Promise<Alert[]> {
    return this.request('/api/v1/alerts')
  }
  dismissAlert(id: string): Promise<{ removed: boolean }> {
    return this.request('/api/v1/alerts/dismiss', 'POST', { id })
  }
  history(): Promise<HistoryEntry[]> {
    return this.request('/api/v1/history')
  }
  unlockArchive(input: ArchiveUnlock): Promise<{ unlocked: boolean }> {
    return this.request('/api/v1/security/unlock', 'POST', input)
  }
  unlockBackup(secret: string): Promise<{ accepted: boolean }> {
    return this.request('/api/v1/security/backup-unlock', 'POST', { secret })
  }
  archivePassword(input: ArchivePassword): Promise<{ accepted: boolean }> {
    return this.request('/api/v1/security/password', 'POST', input)
  }
  upgradeSecurity(input: SecurityUpgrade): Promise<SecurityState> {
    return this.request('/api/v1/security/key-type', 'POST', input)
  }
  settings(area: SettingsArea): Promise<Setting[]> {
    return this.request('/api/v1/settings/' + area)
  }
  saveSettings(
    area: SettingsArea,
    changes: Record<string, unknown>,
  ): Promise<SettingsResult> {
    return this.request('/api/v1/settings/' + area, 'PATCH', { changes })
  }
  backupSets<T = NativeObject[]>(): Promise<T> {
    return this.request<T>('/api/v1/backup-sets')
  }
  backupSetDetails(): Promise<BackupSet[]> {
    return this.request('/api/v1/backup-sets/details')
  }
  backupSet(id: string): Promise<BackupSet> {
    return this.request('/api/v1/backup-sets/' + identifier(id))
  }
  createBackupSet(name: string): Promise<BackupSet> {
    return this.request('/api/v1/backup-sets', 'POST', { name })
  }
  deleteBackupSet(id: string): Promise<{ removed: boolean }> {
    return this.request('/api/v1/backup-sets/' + identifier(id), 'DELETE', {
      confirm: id,
    })
  }
  saveBackupSet(
    id: string,
    changes: Record<string, unknown>,
  ): Promise<SetResult> {
    return this.request('/api/v1/backup-sets/' + identifier(id), 'PATCH', {
      changes,
    })
  }
  changeSelection(id: string, changes: SelectionPath[]): Promise<SetResult> {
    return this.request(
      '/api/v1/backup-sets/' + identifier(id) + '/selection',
      'PATCH',
      { changes },
    )
  }
  browseSource(id: string, parent = '', offset = 0): Promise<SourcePage> {
    return this.request(
      '/api/v1/backup-sets/' + identifier(id) + '/selection/browse',
      'POST',
      { parent, offset, limit: 200 },
    )
  }
  setExcludes(id: string, patterns: BackupSet['excludes']): Promise<SetResult> {
    return this.request(
      '/api/v1/backup-sets/' + identifier(id) + '/excludes',
      'PUT',
      { patterns },
    )
  }
  setBackupDestination(
    id: string,
    target: string,
    attached: boolean,
  ): Promise<SetResult> {
    return this.request(
      '/api/v1/backup-sets/' +
        identifier(id) +
        '/destinations/' +
        identifier(target),
      'PUT',
      { attached },
    )
  }
  health<T = NativeObject>(): Promise<T> {
    return this.request<T>('/api/v1/health')
  }
  scans(): Promise<ScanOperation[]> {
    return this.request('/api/v1/scans')
  }
  scan(backupSetId: string): Promise<ScanOperation> {
    return this.request(
      '/api/v1/backup-sets/' +
        encodeURIComponent(identifier(backupSetId)) +
        '/scan',
      'POST',
      {},
    )
  }
  acknowledgeScan(id: string): Promise<{ acknowledged: boolean }> {
    return this.request(
      '/api/v1/scans/' + encodeURIComponent(id) + '/acknowledge',
      'POST',
      {},
    )
  }
  accountSetup<T = NativeObject>(): Promise<T> {
    return this.request<T>('/api/v1/account/setup')
  }
  migration(): Promise<MigrationState> {
    return this.request('/api/v1/account/migration')
  }
  selectMigration(selectedComputerGuid: string): Promise<LoginResult> {
    return this.request('/api/v1/account/migration/selection', 'POST', {
      selectedComputerGuid,
      confirm: selectedComputerGuid,
    })
  }
  migrationCandidates(): Promise<MigrationState> {
    return this.request('/api/v1/account/migration/candidates')
  }
  inheritDevice(sourceComputerGuid: string): Promise<{ accepted: boolean }> {
    return this.request('/api/v1/account/migration/authorized', 'POST', {
      sourceComputerGuid,
      confirm: sourceComputerGuid,
    })
  }
  replacementState(): Promise<ReplacementState> {
    return this.request('/api/v1/account/replace-device')
  }
  completeReplacement(
    action: 'finish' | 'cancel',
    confirm: string,
  ): Promise<ReplacementState> {
    return this.request('/api/v1/account/replace-device/' + action, 'POST', {
      confirm,
    })
  }
  replaceDevice(sourceComputerGuid: string): Promise<{ accepted: boolean }> {
    return this.request('/api/v1/account/replace-device', 'POST', {
      sourceComputerGuid,
      confirm: sourceComputerGuid,
    })
  }
  accountConfiguration(
    info: LoginInfo,
  ): Promise<{ loginType: 'local' | 'cloud_sso' }> {
    return this.request('/api/v1/account/configuration', 'POST', info)
  }
  accountSSO(
    info: LoginInfo,
  ): Promise<{ id: string; providers: { name: string; url: string }[] }> {
    return this.request('/api/v1/account/sso', 'POST', info)
  }
  accountSSOStatus(
    id: string,
  ): Promise<{ state: string; assertionId?: string; username?: string; differs?: boolean; result?: LoginResult }> {
    return this.request('/api/v1/account/sso/' + encodeURIComponent(id))
  }
  confirmSSO(id: string, assertionId: string, username: string): Promise<LoginResult> {
    return this.request('/api/v1/account/sso/' + encodeURIComponent(id) + '/confirm', 'POST', {assertionId, username})
  }
  discardSSO(id: string, assertionId: string): Promise<void> {
    return this.request('/api/v1/account/sso/' + encodeURIComponent(id) + '/discard', 'POST', {assertionId})
  }
  cancelSSO(id: string): Promise<void> {
    return this.request(
      '/api/v1/account/sso/' + encodeURIComponent(id),
      'DELETE',
    )
  }
  restartEngine(): Promise<{ accepted: boolean }> {
    return this.request('/api/v1/engine/restart', 'POST', {})
  }
  restoreRoots(): Promise<RestoreRoot[]> {
    return this.request('/api/v1/restore/roots')
  }
  archiveSets(
    sourceId: string,
    targetId: string,
  ): Promise<{ id: string; name: string }[]> {
    return this.request('/api/v1/restore/sets', 'POST', {
      sourceId: identifier(sourceId),
      targetId: identifier(targetId),
    })
  }
  restoreJobs(): Promise<RestoreJob[]> {
    return this.request('/api/v1/restore/jobs')
  }
  restoreOperations(): Promise<RestoreOperation[]> {
    return this.request('/api/v1/restore/operations')
  }
  archiveRestoreOperations(
    requestIds: string[],
  ): Promise<{ archived: number }> {
    return this.request('/api/v1/restore/operations/archive', 'POST', {
      requestIds,
      confirm: 'ARCHIVE',
    })
  }
  changeDevice(
    request: DeviceOperationRequest,
  ): Promise<LocalArchiveOperation> {
    return this.request('/api/v1/devices/operations', 'POST', request)
  }
  localArchivePolicy(): Promise<LocalArchivePolicy> {
    return this.request('/api/v1/destinations/local/policy')
  }
  localArchive(request: LocalArchiveRequest): Promise<LocalArchiveOperation> {
    return this.request('/api/v1/destinations/local', 'POST', request)
  }
  restoreHelper(): Promise<{ state: string; reason?: string }> {
    return this.request('/api/v1/restore/helper')
  }
  browseArchive(query: ArchiveQuery): Promise<ArchivePage> {
    return this.request('/api/v1/restore/browse', 'POST', query)
  }
  searchArchive(
    query: ArchiveQuery & { pattern: string; fullPath: boolean },
  ): Promise<ArchivePage> {
    return this.request('/api/v1/restore/search', 'POST', query)
  }
  archiveContents(query: ArchiveQuery): Promise<{
    files: string
    directories: string
    bytes: string
    done: boolean; verified: boolean
  }> {
    return this.request('/api/v1/restore/contents', 'POST', query)
  }
  archiveVersions(query: ArchiveQuery): Promise<ArchiveVersion[]> {
    return this.request('/api/v1/restore/versions', 'POST', query)
  }
  startRestore(request: RestoreRequest): Promise<RestoreOperation> {
    return this.request('/api/v1/restore/jobs', 'POST', request)
  }
  async startOriginalRestore(
    request: RestoreRequest,
  ): Promise<RestoreOperation> {
    const {
      requestId,
      sourceId,
      targetId,
      existing,
      permissions,
      includeDeleted,
      items,
      excluded,
    } = request
    const result = await this.request<{
      operation: RestoreOperation
      complete: boolean
    }>('/api/v1/restore/original', 'POST', {
      requestId,
      sourceId,
      targetId,
      existing,
      permissions,
      includeDeleted,
      items,
      excluded,
    })
    return result.operation
  }
  controlRestore(
    id: string,
    action: 'start' | 'stop' | 'remove',
  ): Promise<{ accepted: boolean }> {
    const path = '/api/v1/restore/jobs/' + encodeURIComponent(identifier(id))
    return action === 'remove'
      ? this.request(path, 'DELETE')
      : this.request(path + '/' + action, 'POST', {})
  }
  accountLogin(info: LoginInfo, password: string): Promise<LoginResult> {
    return this.request('/api/v1/account/login', 'POST', { ...info, password })
  }
}

export const api = new API()
