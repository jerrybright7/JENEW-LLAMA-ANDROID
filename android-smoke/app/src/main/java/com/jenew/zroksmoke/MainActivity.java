package com.jenew.zroksmoke;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;
import org.json.JSONObject;

public final class MainActivity extends Activity {
    private static final String TAG = "ZrokSmoke";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        TextView resultView = new TextView(this);
        resultView.setPadding(32, 48, 32, 32);
        resultView.setTextSize(18);
        resultView.setText("Starting isolated zrok Go/JNI smoke test...");
        setContentView(resultView);

        try {
            // Go Mobile requires the Android context before native SDK calls.
            go.Seq.setContext(getApplicationContext());
            bridge.Bridge.touch();

            String probe = bridge.Bridge.sdkCompileProbe();
            if (probe == null || !probe.contains("zrok SDK v2 bridge ready")) {
                throw new IllegalStateException("Unexpected bridge probe: " + probe);
            }

            // Exercise real filesystem initialization inside app-private storage.
            String privateRoot = getFilesDir().getAbsolutePath() + "/zrok2";
            bridge.Bridge.initialize(privateRoot);
            JSONObject status = new JSONObject(bridge.Bridge.status());
            if (!status.optBoolean("initialized", false)) {
                throw new IllegalStateException("Bridge did not initialize its private environment");
            }
            if (status.optBoolean("enabled", true)) {
                throw new IllegalStateException("Fresh smoke environment should not be enabled");
            }

            Log.i(TAG, "ZROK_SMOKE_PASS: " + probe + "; status=" + status);
            resultView.setText("PASS\\n\\n" + probe
                    + "\\n\\nAndroid JNI loaded, app-private storage initialized, "
                    + "and non-secret status returned: " + status);
        } catch (Throwable error) {
            Log.e(TAG, "ZROK_SMOKE_FAIL", error);
            resultView.setText("FAIL\\n\\n" + error.getClass().getName()
                    + ": " + error.getMessage());
        }
    }
}
