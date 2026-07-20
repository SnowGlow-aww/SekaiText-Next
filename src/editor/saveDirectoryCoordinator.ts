import { SaveCoordinator } from './saveCoordinator'

// File rename/ensure/save and save-root migration must share one frontend queue.
// The backend generation fence rejects stale requests as a second line of defense.
export const saveDirectoryCoordinator = new SaveCoordinator()
