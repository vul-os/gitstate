// Brand icon marks for notification channel types. Each is a drop-in component
// matching the lucide icon API (a `size` prop → width/height), so it can be used
// interchangeably with lucide icons in the channel pickers and rows.
//
// The marks use their brand colors by default (Discord blurple, Google Chat
// green, Teams purple). Pass a `className` to recolor (e.g. for the muted row
// avatar) — `fill="currentColor"` paths follow the text color when no explicit
// fill is set in the path.

// Discord — official "Clyde" mark, blurple (#5865F2).
export function DiscordIcon({ size = 16, className }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M20.317 4.369a19.79 19.79 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.249a18.27 18.27 0 0 0-5.487 0 12.6 12.6 0 0 0-.617-1.25.077.077 0 0 0-.079-.036A19.74 19.74 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 0 0-.041-.106 13.1 13.1 0 0 1-1.872-.892.077.077 0 0 1-.008-.128c.126-.094.252-.192.372-.291a.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.061 0a.074.074 0 0 1 .078.009c.12.099.246.198.373.292a.077.077 0 0 1-.006.127 12.3 12.3 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.84 19.84 0 0 0 6.002-3.03.077.077 0 0 0 .032-.055c.5-5.177-.838-9.674-3.549-13.66a.06.06 0 0 0-.031-.028zM8.02 15.331c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
    </svg>
  )
}

// Google Chat — speech bubble in Google's four brand colors.
export function GoogleChatIcon({ size = 16, className }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      className={className}
      aria-hidden="true"
    >
      <path
        fill="#00832D"
        d="M1.637 0C.733 0 0 .733 0 1.637v16.5c0 .904.733 1.636 1.637 1.636h2.522v3.86a.41.41 0 0 0 .685.305l4.582-4.165H18.07c.904 0 1.637-.732 1.637-1.636V1.637C19.707.733 18.974 0 18.07 0z"
      />
      <path
        fill="#fff"
        d="M14.61 8.604H5.097a.96.96 0 0 0 0 1.92h9.513a.96.96 0 0 0 0-1.92zm-3.36 3.84H5.097a.96.96 0 0 0 0 1.92h6.153a.96.96 0 0 0 0-1.92z"
      />
    </svg>
  )
}

// Microsoft Teams — the "T" people mark, Teams purple (#5059C9).
export function TeamsIcon({ size = 16, className }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path
        fill="#5059C9"
        d="M19.19 8.45h.42a2.18 2.18 0 0 1 2.18 2.18v3.51a2.62 2.62 0 0 1-2.62 2.62h-.01a2.62 2.62 0 0 1-2.62-2.62V9.13c0-.38.3-.68.68-.68zm1.07-1.2a1.86 1.86 0 1 0 0-3.72 1.86 1.86 0 0 0 0 3.72z"
      />
      <path
        fill="#7B83EB"
        d="M14.06 7.25a2.62 2.62 0 1 0 0-5.25 2.62 2.62 0 0 0 0 5.25zm3.39 1.2h-7.2c-.45.01-.8.39-.79.84v6.49a4.4 4.4 0 0 0 3.4 4.32 4.27 4.27 0 0 0 5.13-4.18V9.33a.88.88 0 0 0-.88-.88z"
      />
      <path
        fill="#4B53BC"
        d="M12.66 8.45v8.13a4.06 4.06 0 0 1-3.16 3.95 4.07 4.07 0 0 1-.74.07 4.06 4.06 0 0 1-3.9-2.9.84.84 0 0 1-.02-.07H8.4c.45 0 .82-.36.82-.81V8.45z"
        opacity="0.001"
      />
      <path
        fill="#000"
        opacity="0.1"
        d="M11.86 8.45v8.93c0 .32-.04.63-.12.94a3.7 3.7 0 0 1-3.58 2.78H4.69a4.07 4.07 0 0 1-.45-1.27H8.3c.45 0 .82-.36.82-.81V8.45z"
      />
      <path
        fill="#7B83EB"
        d="M1.31 6.6h7.78c.72 0 1.31.58 1.31 1.3v7.79c0 .72-.59 1.3-1.31 1.3H1.31C.59 17 0 16.42 0 15.7V7.9c0-.72.59-1.3 1.31-1.3z"
      />
      <path
        fill="#fff"
        d="M7.23 9.57H5.67v4.25H4.68V9.57H3.13v-.82h4.1z"
      />
    </svg>
  )
}
