export function ProjectNotice() {
  const repositoryURL = 'https://github.com/traktuner/docker-crashplan-headless'
  const revision = encodeURIComponent(import.meta.env.VITE_SOURCE_REVISION || 'main')

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
    </footer>
  )
}
