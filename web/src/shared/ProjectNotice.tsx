export function ProjectNotice() {
  const repositoryURL = 'https://github.com/traktuner/docker-crashplan-headless'
  const rawRevision = import.meta.env.VITE_SOURCE_REVISION || 'main'
  const revision = encodeURIComponent(rawRevision)
  const version = import.meta.env.VITE_SOURCE_VERSION || 'dev'
  const commit = /^[0-9a-f]{40}$/.test(rawRevision) ? rawRevision.slice(0, 7) : rawRevision

  return (
    <footer className="project-notice" aria-label="Project disclaimer">
      <p>
        CrashPlan Headless is an independent, unofficial project. It is not
        affiliated with, endorsed by, or sponsored by CrashPlan.
      </p>
      <p>
        CrashPlan and related product names are trademarks of their respective
        owners.
      </p>
      <p>
        Copyright © 2026 traktuner. No warranty. You may redistribute this project
        under the <a href={repositoryURL + '/blob/' + revision + '/LICENSE'}>AGPLv3</a>.{' '}
        <a href={repositoryURL + '/tree/' + revision}>Source code</a>
      </p>
      <p className="mono">
        Version {version} ·{' '}
        <a href={repositoryURL + '/commit/' + revision} title={rawRevision}>
          commit {commit}
        </a>
      </p>
    </footer>
  )
}
