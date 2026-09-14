package index

import (
	"os"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func countType(els []store.Element, typ string) int {
	n := 0
	for _, e := range els {
		if e.ElementType == typ {
			n++
		}
	}
	return n
}

func countRels(rels []store.Relationship, typ string) int {
	n := 0
	for _, r := range rels {
		if r.RelType == typ {
			n++
		}
	}
	return n
}

func hasQN(els []store.Element, qn string) bool {
	for _, e := range els {
		if e.QualifiedName == qn {
			return true
		}
	}
	return false
}

func hasTypedName(els []store.Element, typ, name string) bool {
	for _, e := range els {
		if e.ElementType == typ && e.Name == name {
			return true
		}
	}
	return false
}

func TestExtractManifestActivity(t *testing.T) {
	src := `
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <activity android:name=".MainActivity" />
    </application>
</manifest>`
	els, _ := ExtractAndroidManifest("AndroidManifest.xml", []byte(src))
	var names []string
	for _, e := range els {
		if e.ElementType == "android_activity" {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		t.Fatal("should extract activity")
	}
	if names[0] != ".MainActivity" {
		t.Fatalf("activity name = %q, want .MainActivity", names[0])
	}
}

func TestExtractManifestService(t *testing.T) {
	src := `
<manifest>
    <service android:name=".MyService" android:exported="false" />
</manifest>`
	els, _ := ExtractAndroidManifest("AndroidManifest.xml", []byte(src))
	if countType(els, "android_service") == 0 {
		t.Fatal("should extract service")
	}
}

func TestExtractManifestPermission(t *testing.T) {
	src := `
<manifest>
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.ACCESS_FINE_LOCATION" />
</manifest>`
	els, rels := ExtractAndroidManifest("AndroidManifest.xml", []byte(src))
	if got := countType(els, "android_permission"); got != 2 {
		t.Fatalf("permissions = %d, want 2", got)
	}
	if got := countRels(rels, "requires_permission"); got != 2 {
		t.Fatalf("requires_permission = %d, want 2", got)
	}
}

func TestExtractManifestFull(t *testing.T) {
	src := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.myapp">

    <uses-permission android:name="android.permission.INTERNET" />

    <application
        android:label="@string/app_name"
        android:theme="@style/AppTheme">
        <activity
            android:name=".MainActivity"
            android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
        <service android:name=".BackgroundService" />
    </application>

</manifest>`
	els, rels := ExtractAndroidManifest("AndroidManifest.xml", []byte(src))
	if got := countType(els, "android_activity"); got != 1 {
		t.Fatalf("activities = %d, want 1", got)
	}
	if got := countType(els, "android_service"); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if got := countType(els, "android_permission"); got != 1 {
		t.Fatalf("permissions = %d, want 1", got)
	}
	if got := countRels(rels, "declares_component"); got != 2 {
		t.Fatalf("declares_component = %d, want 2", got)
	}
	if got := countType(els, "android_intent_filter"); got != 1 {
		t.Fatalf("intent filters = %d, want 1", got)
	}
}

func TestExtractManifestReceiverAndProvider(t *testing.T) {
	receiverSrc := `
<manifest>
    <receiver android:name=".MyReceiver" android:exported="false">
        <intent-filter>
            <action android:name="android.intent.action.BOOT_COMPLETED" />
        </intent-filter>
    </receiver>
</manifest>`
	els, _ := ExtractAndroidManifest("AndroidManifest.xml", []byte(receiverSrc))
	if countType(els, "android_broadcast_receiver") == 0 {
		t.Fatal("should extract receiver")
	}

	providerSrc := `
<manifest>
    <provider
        android:name=".MyContentProvider"
        android:authorities="com.example.provider"
        android:exported="false" />
</manifest>`
	els, _ = ExtractAndroidManifest("AndroidManifest.xml", []byte(providerSrc))
	if countType(els, "android_content_provider") == 0 {
		t.Fatal("should extract provider")
	}
}

func TestExtractManifestFixture(t *testing.T) {
	els, rels := ExtractAndroidManifest("AndroidManifest.xml", mustReadFixture(t, "AndroidManifest.xml"))
	if got := countType(els, "android_activity"); got != 2 {
		t.Fatalf("activities = %d, want 2", got)
	}
	if got := countType(els, "android_service"); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if got := countType(els, "android_broadcast_receiver"); got != 1 {
		t.Fatalf("receivers = %d, want 1", got)
	}
	if got := countType(els, "android_content_provider"); got != 1 {
		t.Fatalf("providers = %d, want 1", got)
	}
	if got := countType(els, "android_permission"); got != 1 {
		t.Fatalf("permissions = %d, want 1", got)
	}
	if got := countType(els, "android_feature"); got != 1 {
		t.Fatalf("features = %d, want 1", got)
	}
	if got := countType(els, "android_intent_filter"); got != 2 {
		t.Fatalf("intent filters = %d, want 2", got)
	}
	if got := countRels(rels, "declares_component"); got != 5 {
		t.Fatalf("declares_component = %d, want 5", got)
	}
	if got := countRels(rels, "requires_permission"); got != 1 {
		t.Fatalf("requires_permission = %d, want 1", got)
	}
	if got := countRels(rels, "declares_feature"); got != 1 {
		t.Fatalf("declares_feature = %d, want 1", got)
	}
	if got := countRels(rels, "has_application_class"); got != 0 {
		t.Fatalf("has_application_class = %d, want 0 (application has no android:name)", got)
	}
	if got := countType(els, "android_metadata"); got != 1 {
		t.Fatalf("metadata entries = %d, want 1", got)
	}
	want := "__android__activity___MainActivity"
	if !hasQN(els, want) {
		t.Fatalf("missing qualified name %s", want)
	}
	if !hasTypedName(els, "android_manifest", "AndroidManifest.xml") {
		t.Fatal("missing android_manifest file element")
	}
}
