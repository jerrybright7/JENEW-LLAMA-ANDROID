package com.jenew.zroksmoke;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;

import java.io.File;

public final class MainActivity extends Activity {
    private static final String TAG = "ZrokSmoke";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        TextView resultView = new TextView(this);
        resultView.setPadding(32, 48, 32, 32);
        resultView.setTextSize(18);
        resultView.setText("Starting isolated zrok Go/JNI runtime smoke test...");
        setContentView(resultView);

        try {
            // Go Mobile requires Android context before SDK calls.
            go.Seq.setContext(getApplicationContext());
            bridge.Bridge.touch();

            String probe = bridge.Bridge.sdkCompileProbe();
            if (probe == null || !probe.contains("zrok SDK v2 bridge ready")) {
                throw new IllegalStateException("Unexpected bridge response: " + probe);
            }

            // Exercise real Go filesystem initialization inside app-private storage.
            String privateZrokRoot = new File(getFilesDir(), "zrok2").getAbsolutePath();
            bridge.Bridge.initialize(privateZrokRoot);

            String status = bridge.Bridge.status();
            if (status == null
                    || !status.contains("\"initialized\":true")
                    || !status.contains("\"enabled\":false")) {
                throw new IllegalStateException("Unexpected initialized status: " + status);
            }

            String result = probe + "\n" + status;
            Log.i(TAG, "ZROK_SMOKE_PASS: " + result);
            resultView.setText("PASS\n\n" + result
                    + "\n\nGo JNI loaded, app-private root initialized, and status returned.");
        } catch (Throwable error) {
            Log.e(TAG, "ZROK_SMOKE_FAIL", error);
            resultView.setText("FAIL\n\n" + error.getClass().getName() + ": " + error.getMessage());
        }
    }
}
