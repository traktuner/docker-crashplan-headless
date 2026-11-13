import type { LocalArchivePolicy, LocalArchiveRequest } from '../../shared/api'
export function allowedArchiveSources(
  policy: LocalArchivePolicy | undefined,
  action: LocalArchiveRequest['action'] = 'attach',
) {
  return (
    policy?.devices.filter((device) =>
      device.self
        ? policy.allowed
        : device.own &&
          (policy.ownDevices ||
            (action === 'attach' && policy.canAttachOwn === true)),
    ) ?? []
  )
}
