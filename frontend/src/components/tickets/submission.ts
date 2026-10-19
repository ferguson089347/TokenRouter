// submission.ts assigns retry identities to ticket drafts using text and file
// object identity. Repeating an unchanged draft reuses its key, allowing an
// uncertain network result to be retried against persisted server deduplication.
//
// A changed payload, reordered file, or replacement File object starts a new
// identity. The utility snapshots the file array so parent list edits are
// detected even when they mutate the original array. Successful submission
// resets its retained state, and each key uses cryptographically generated
// random bytes to distinguish separate form submissions across browser sessions.
// ticketSubmission creates a draft-local retry key tracker with an explicit
// reset operation. Callers keep one tracker for the form lifetime and reset it
// after successful submission or intentional draft replacement.
export function ticketSubmission() {
  let previousPayload = ''
  let previousFiles: File[] = []
  let key = ''
  return {
    // key returns the retained retry identity while payload and file identities
    // match the previous draft. It serializes the payload and compares every
    // ordered File object, detecting replacements even when filenames match.
    //
    // A changed draft receives a cryptographically random identity and a fresh
    // file-array snapshot. Keeping that snapshot separate from the caller array
    // makes later list mutation visible. Callers reuse the result after uncertain
    // network failures and invoke reset after a committed submission succeeds.
    key(payload: unknown, files: File[]): string {
      const serialized = JSON.stringify(payload)
      if (
        !key ||
        serialized !== previousPayload ||
        files.length !== previousFiles.length ||
        files.some((file, index) => file !== previousFiles[index])
      ) {
        key = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
          byte.toString(16).padStart(2, '0'),
        ).join('')
        previousPayload = serialized
        previousFiles = [...files]
      }
      return key
    },
    // reset releases the retained draft identity after success or intentional replacement.
    reset() {
      key = ''
      previousPayload = ''
      previousFiles = []
    },
  }
}
