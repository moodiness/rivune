import Foundation

@inline(__always)
func rivuneLocalized(_ key: String) -> String {
    Bundle.main.localizedString(forKey: key, value: key, table: nil)
}

func rivuneLocalizedFormat(_ key: String, _ arguments: CVarArg...) -> String {
    String(format: rivuneLocalized(key), locale: Locale.current, arguments: arguments)
}
