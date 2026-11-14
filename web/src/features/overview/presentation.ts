export function engineReady(value: unknown): boolean | undefined {
  if (value === undefined) return undefined
  if (
    !value ||
    typeof value !== 'object' ||
    !('status' in value) ||
    value.status !== 'running' ||
    !('native' in value)
  )
    return false
  const native = value.native
  return (
    !!native &&
    typeof native === 'object' &&
    'nativeReady' in native &&
    native.nativeReady === true &&
    'eventsReady' in native &&
    native.eventsReady === true
  )
}
