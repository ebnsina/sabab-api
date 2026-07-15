/**
 * Every icon the dashboard uses, named for its role rather than its shape.
 *
 * Centralised for two reasons: components import a role ("Resolve", "Search")
 * instead of a HugeIcons SKU like CheckmarkCircle02Icon, so the UI reads
 * clearly; and swapping icon library or picking a different glyph is a one-line
 * change here rather than a hunt through every component.
 *
 * Only the specific icons are imported, never a wildcard, so tree-shaking keeps
 * the bundle to what we actually render.
 */
export { HugeiconsIcon } from '@hugeicons/svelte';

export {
	Activity03Icon as BrandIcon,
	Loading03Icon as SpinnerIcon,
	PackageIcon as ProjectsIcon,
	CircleIcon as ProjectDotIcon,
	Logout01Icon as LogoutIcon,
	InboxIcon as EmptyIcon,
	Search01Icon as SearchIcon,
	UserMultiple02Icon as UsersIcon,
	HashIcon as ReleaseIcon,
	CheckmarkCircle02Icon as ResolveIcon,
	Notification03Icon as UnresolvedIcon,
	ViewOffIcon as IgnoreIcon,
	ArrowRight01Icon as ChevronRightIcon,
	ArrowDown01Icon as ChevronDownIcon,
	Alert02Icon as AlertIcon,
	Home03Icon as HomeIcon,
	RotateClockwiseIcon as ReopenIcon,
	Clock01Icon as ClockIcon,
	Tag01Icon as TagIcon,
	Globe02Icon as TraceIcon,
	ComputerIcon as DeviceIcon,
	ArrowLeft01Icon as BackIcon
} from '@hugeicons/core-free-icons';
